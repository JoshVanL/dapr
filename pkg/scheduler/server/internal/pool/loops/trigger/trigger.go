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

package trigger

import (
	"context"
	"fmt"
	"sync"

	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/pkg/proto/scheduler/v1"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops/connections/store"
	"github.com/dapr/kit/events/loop"
	"github.com/dapr/kit/logger"
	"github.com/diagridio/go-etcd-cron/api"
	"google.golang.org/protobuf/types/known/anypb"
)

var log = logger.NewLogger("dapr.scheduler.server.pool.loops.trigger")

//var (
//	StreamLoopCache = sync.Pool{New: func() any {
//		return loop.Empty[loops.Event]()
//	}}
//	streamCache = sync.Pool{New: func() any {
//		return &stream{
//			inflight: make(map[uint64]func(api.TriggerResponseResult)),
//		}
//	}}
//)

type Options struct {
	//IDx      uint64
	//Channel  schedulerv1pb.Scheduler_WatchJobsServer
	//Request  *schedulerv1pb.WatchJobsRequestInitial
	//ConnLoop loop.Interface[loops.Event]
	StreamPool *store.Namespace
}

// TODO: @joshvanl
type trigger struct {
	streamPool *store.Namespace
}

func New(opts Options) loop.Interface[loops.Event] {
	trig := &trigger{
		streamPool: opts.StreamPool,
	}

	return loop.New(trig, 5)

}

func (t *trigger) Handle(ctx context.Context, event loops.Event) error {
	switch e := event.(type) {
	case *loops.TriggerRequest:
		return t.handleTriggerRequest(e)
	case *loops.BroadcastAddJob:
		return t.handleBroadcastAdd(e)
	case *loops.BroadcastDeleteJob:
		return t.handleBroadcastDelete(e)
	//case *loops.StreamAddJob:

	//case *loops.TriggerRequest:
	//	s.handleTriggerRequest(e)
	//case *loops.StreamShutdown:
	//	s.handleShutdown()
	default:
		return fmt.Errorf("unknown trigger event type: %T", e)
	}

	return nil
}

// handleTriggerRequest handles a trigger request for a job.
func (t *trigger) handleTriggerRequest(req *loops.TriggerRequest) error {
	streams, ok := t.getStreamLoops(req.Job.GetMetadata())
	if !ok {
		fmt.Printf(">>COULDN'T FIND LOOP FOR JOB: %s\n", req.Job.GetKey())
		req.ResultFn(api.TriggerResponseResult_UNDELIVERABLE)
		return nil
	}

	if req.Job.Metadata.GetTarget().GetBroadcast() != nil {
		var err error
		req.Job.Data, err = anypb.New(&schedulerv1pb.BroadcastJobDataWrapper{
			Type: schedulerv1pb.BroadcastJobEventType_BROADCAST_JOB_EVENT_TRIGGER,
			Data: req.Job.GetData(),
		})
		if err != nil {
			return err
		}
	}

	var resp *api.TriggerResponseResult
	var lock sync.Mutex
	var wg sync.WaitGroup
	wg.Add(len(streams))
	for _, stream := range streams {
		stream.Enqueue(&loops.TriggerRequest{
			Job: req.Job,
			ResultFn: func(result api.TriggerResponseResult) {
				defer wg.Done()
				lock.Lock()
				defer lock.Unlock()
				switch result {
				case api.TriggerResponseResult_UNDELIVERABLE:
					resp = &result
				case api.TriggerResponseResult_FAILED:
					resp = &result
				case api.TriggerResponseResult_SUCCESS:
					if resp == nil {
						resp = &result
					}
				}
			},
		})
	}

	wg.Wait()
	req.ResultFn(*resp)
	return nil
}

// TODO: @joshvanl
func (t *trigger) handleBroadcastAdd(req *loops.BroadcastAddJob) error {
	data, err := anypb.New(&scheduler.BroadcastJobDataWrapper{
		Type: scheduler.BroadcastJobEventType_BROADCAST_JOB_EVENT_PUT,
		Data: req.Job.GetData(),
	})
	if err != nil {
		return err
	}

	// TODO: @joshvanl: resultFN
	t.sendBroadcastEvent(data, req.Job)
	return nil
}

func (t *trigger) handleBroadcastDelete(req *loops.BroadcastDeleteJob) error {
	data, err := anypb.New(&scheduler.BroadcastJobDataWrapper{
		Type: scheduler.BroadcastJobEventType_BROADCAST_JOB_EVENT_DELETE,
	})
	if err != nil {
		return err
	}
	t.sendBroadcastEvent(data, req.Job)
	return nil
}

func (t *trigger) sendBroadcastEvent(data *anypb.Any, job *internalsv1pb.JobEvent) {
	streams := t.getAllStreamLoop(job.GetMetadata())

	fmt.Printf(">>ADDING WAITGROUP FOR BROADCAST: %s %s %d\n", job.GetKey(), data, len(streams))
	var wg sync.WaitGroup
	wg.Add(len(streams))

	sendReq := &loops.TriggerRequest{
		Job: &internalsv1pb.JobEvent{
			Key:      job.GetKey(),
			Name:     job.GetName(),
			Metadata: job.GetMetadata(),
			Data:     data,
		},
		ResultFn: func(result api.TriggerResponseResult) {
			fmt.Printf(">>> connections.sendBroadcastEvent RESULT: %s\n", result)
			fmt.Printf(">>DONE WAITGROUP FOR BROADCAST: %s %s %d\n", job.GetKey(), data, len(streams))
			wg.Done()
		},
	}

	for _, loop := range streams {
		fmt.Printf(">>> connections.sendBroadcastEvent: %T\n", loop)
		loop.Enqueue(sendReq)
	}
	wg.Wait()
}

// getStreamLoops returns a stream loop from the pool based on the metadata.
func (t *trigger) getStreamLoops(meta *schedulerv1pb.JobMetadata) ([]loop.Interface[loops.Event], bool) {
	switch m := meta.GetTarget(); m.GetType().(type) {
	case *schedulerv1pb.JobTargetMetadata_Job:
		l, ok := t.streamPool.AppID(meta.GetNamespace(), meta.GetAppId())
		return []loop.Interface[loops.Event]{l}, ok
	case *schedulerv1pb.JobTargetMetadata_Actor:
		l, ok := t.streamPool.ActorType(meta.GetNamespace(), m.GetActor().GetType())
		return []loop.Interface[loops.Event]{l}, ok
	case *schedulerv1pb.JobTargetMetadata_Broadcast:
		switch b := m.GetBroadcast().GetBroadcast().(type) {
		case *schedulerv1pb.TargetBroadcast_DurableActorId:
			return t.streamPool.AllActorTypes(meta.GetNamespace(), b.DurableActorId.Type), true
		}
		log.Errorf("unsupported broadcast type: %T", m.GetBroadcast().GetBroadcast())
		return nil, false
	default:
		return nil, false
	}
}

// getAllStreamLoops returns all stream loops from the pool based on the metadata.
func (t *trigger) getAllStreamLoop(meta *schedulerv1pb.JobMetadata) []loop.Interface[loops.Event] {
	switch m := meta.GetTarget(); m.GetType().(type) {
	case *schedulerv1pb.JobTargetMetadata_Job:
		return t.streamPool.AllAppIDs(meta.GetNamespace(), meta.GetAppId())
	case *schedulerv1pb.JobTargetMetadata_Actor:
		return t.streamPool.AllActorTypes(meta.GetNamespace(), m.GetActor().GetType())
	case *schedulerv1pb.JobTargetMetadata_Broadcast:
		switch b := m.GetBroadcast().GetBroadcast().(type) {
		case *schedulerv1pb.TargetBroadcast_DurableActorId:
			return t.streamPool.AllActorTypes(meta.GetNamespace(), b.DurableActorId.Type)
		}
	}
	return nil
}
