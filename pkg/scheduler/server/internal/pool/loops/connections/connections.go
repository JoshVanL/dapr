/*
Copyright 2025 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package connections

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/diagridio/go-etcd-cron/api"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops/connections/store"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops/stream"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops/trigger"
	"github.com/dapr/kit/events/loop"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.scheduler.server.pool.loops.connections")

type Options struct {
	Cron       api.Interface
	CancelPool context.CancelCauseFunc
}

// connections is a control loop that creates and manages stream connections,
// piping trigger requests.
type connections struct {
	cron       api.Interface
	cancelPool context.CancelCauseFunc
	loop       loop.Interface[loops.Event]

	streams    map[uint64]context.CancelCauseFunc
	streamIDx  uint64
	streamPool *store.Namespace
	wg         sync.WaitGroup

	triggerLoops map[string]loop.Interface[loops.Event]
}

func New(opts Options) loop.Interface[loops.Event] {
	conns := &connections{
		streams:      make(map[uint64]context.CancelCauseFunc),
		cancelPool:   opts.CancelPool,
		cron:         opts.Cron,
		streamPool:   store.New(),
		triggerLoops: make(map[string]loop.Interface[loops.Event]),
	}

	conns.loop = loop.New(conns, 1024)
	return conns.loop
}

func (c *connections) Handle(ctx context.Context, event loops.Event) error {
	fmt.Printf(">>> connections.Handle: %T\n", event)
	switch e := event.(type) {
	case *loops.ConnAdd:
		return c.handleAdd(ctx, e)
	case *loops.ConnCloseStream:
		c.handleCloseStream(e)
	case *loops.Shutdown:
		c.handleShutdown()

	case *loops.TriggerRequest:
		c.handleTriggerRequest(e)
	case *loops.BroadcastAddJob:
		return c.handleBroadcastAdd(e)
	case *loops.BroadcastDeleteJob:
		return c.handleBroadcastDelete(e)

	default:
		return fmt.Errorf("unknown connections event type: %T", e)
	}

	return nil
}

// handleAdd adds a connection to the pool for a given namespace/appID.
func (c *connections) handleAdd(ctx context.Context, add *loops.ConnAdd) error {
	var prefixes []string
	var appID *string

	reqNamespace := add.Request.GetNamespace()
	reqAppID := add.Request.GetAppId()

	// To account for backwards compatibility where older clients did not use
	// this field, we assume a connected client and implement both app jobs, as
	// well as actor job types. We can remove this in v1.16
	ts := add.Request.GetAcceptJobTypes()
	if len(ts) == 0 || slices.Contains(ts, schedulerv1pb.JobTargetType_JOB_TARGET_TYPE_JOB) {
		log.Infof("Adding a Sidecar connection to Scheduler for appID: %s/%s.", reqNamespace, reqAppID)
		appID = &add.Request.AppId
		prefixes = append(prefixes, "app||"+reqNamespace+"||"+reqAppID+"||")
	}

	if len(ts) == 0 || slices.Contains(ts, schedulerv1pb.JobTargetType_JOB_TARGET_TYPE_ACTOR_REMINDER) {
		for _, actorType := range add.Request.GetActorTypes() {
			log.Infof("Adding a Sidecar connection to Scheduler for actor type: %s/%s.", reqNamespace, actorType)
			prefixes = append(prefixes, "actorreminder||"+reqNamespace+"||"+actorType+"||")
		}
	}

	log.Debugf("Marking deliverable prefixes for Sidecar connection: %s/%s: %v.",
		add.Request.GetNamespace(), add.Request.GetAppId(), prefixes)

	pcancel, err := c.cron.DeliverablePrefixes(ctx, prefixes...)
	if err != nil {
		return err
	}

	log.Debugf("Added a Sidecar connection to Scheduler for: %s/%s.",
		add.Request.GetNamespace(), add.Request.GetAppId())

	streamIDx := c.streamIDx
	c.streamIDx++

	streamLoop := stream.New(stream.Options{
		IDx:      streamIDx,
		Channel:  add.Channel,
		Request:  add.Request,
		ConnLoop: c.loop,
	})

	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		err := streamLoop.Run(ctx)
		if err != nil && !errors.Is(err, context.Canceled) {
			log.Errorf("Error running stream loop: %v", err)
			c.cancelPool(err)
		}
	}()

	c.streams[streamIDx] = c.streamPool.Add(store.Options{
		Namespace:  add.Request.GetNamespace(),
		AppID:      appID,
		ActorTypes: add.Request.GetActorTypes(),
		Connection: &store.StreamConnection{
			Cancel: func(err error) {
				pcancel()
				add.Cancel(err)
			},
			Loop: streamLoop,
		},
	})

	// TODO: @joshvanl: need to send all current broadcast jobs.

	return nil
}

// handleTriggerRequest handles a trigger request for a job.
func (c *connections) handleTriggerRequest(req *loops.TriggerRequest) {
	trig, ok := c.triggerLoops[req.Job.GetKey()]
	if !ok {
		// TODO: @joshvanl: cache
		trig = trigger.New(trigger.Options{
			StreamPool: c.streamPool,
		})
		c.triggerLoops[req.Job.GetKey()] = trig

		go trig.Run(context.TODO())
	}

	trig.Enqueue(req)
}

// handleCloseStream handles a close stream request.
func (c *connections) handleCloseStream(closeStream *loops.ConnCloseStream) error {
	cancel, ok := c.streams[closeStream.StreamIDx]
	if !ok {
		return errors.New("catastrophic state machine error: lost connection stream reference")
	}

	cancel(nil)
	delete(c.streams, closeStream.StreamIDx)
	return nil
}

// handleShutdown handles the shutdown of the connections.
func (c *connections) handleShutdown() {
	defer c.wg.Wait()

	var wg sync.WaitGroup
	wg.Add(len(c.streams))
	for _, cancel := range c.streams {
		go func() {
			cancel(nil)
			wg.Done()
		}()
	}

	wg.Wait()

	c.streams = make(map[uint64]context.CancelCauseFunc)
}

// TODO: @joshvanl
func (c *connections) handleBroadcastAdd(req *loops.BroadcastAddJob) error {
	trig, ok := c.triggerLoops[req.Job.GetKey()]
	if !ok {
		// TODO: @joshvanl: cache
		trig = trigger.New(trigger.Options{
			StreamPool: c.streamPool,
		})
		c.triggerLoops[req.Job.GetKey()] = trig

		go trig.Run(context.TODO())
	}

	trig.Enqueue(req)
	return nil
}

func (c *connections) handleBroadcastDelete(req *loops.BroadcastDeleteJob) error {
	trig, ok := c.triggerLoops[req.Job.GetKey()]
	if !ok {
		// TODO: @joshvanl: cache
		trig = trigger.New(trigger.Options{
			StreamPool: c.streamPool,
		})
		c.triggerLoops[req.Job.GetKey()] = trig

		go trig.Run(context.TODO())
	}

	trig.Enqueue(req)
	return nil
}
