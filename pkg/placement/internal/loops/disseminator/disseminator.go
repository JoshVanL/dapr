/*
Copyright 2026 The Dapr Authors
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

package disseminator

import (
	"context"
	"fmt"
	"sync"

	"github.com/dapr/dapr/pkg/placement/internal/authorizer"
	"github.com/dapr/dapr/pkg/placement/internal/loops"
	"github.com/dapr/dapr/pkg/placement/internal/loops/disseminator/store"
	"github.com/dapr/dapr/pkg/placement/internal/loops/disseminator/timeout"
	"github.com/dapr/dapr/pkg/placement/internal/loops/stream"
	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	"github.com/dapr/kit/events/loop"
	"github.com/dapr/kit/events/queue"
	"github.com/dapr/kit/ptr"
)

var (
	loopFactory = loop.New[loops.Event](1024)
	dissCache   = sync.Pool{New: func() any {
		return &disseminator{
			streams: make(map[uint64]*streamConn),
		}
	}}
)

type Options struct {
	NamespaceLoop     loop.Interface[loops.Event]
	ReplicationFactor int64
	Authorizer        *authorizer.Authorizer

	Namespace string
}

type streamConn struct {
	loop           loop.Interface[loops.Event]
	currentState   *v1pb.HostOperation
	currentVersion *uint64
}

// disseminator is a control loop that creates and manages stream connections,
// disseminating actor type updates with a 3 stage lock.
type disseminator struct {
	nsLoop     loop.Interface[loops.Event]
	loop       loop.Interface[loops.Event]
	authorizer *authorizer.Authorizer

	timeoutQ *queue.Processor[uint64, *timeout.Dissemination]

	streams   map[uint64]*streamConn
	store     *store.Store
	streamIDx uint64
	wg        sync.WaitGroup

	currentOperation v1pb.HostOperation
	currentVersion   uint64
}

func New(opts Options) loop.Interface[loops.Event] {
	diss := dissCache.Get().(*disseminator)

	diss.nsLoop = opts.NamespaceLoop
	diss.authorizer = opts.Authorizer
	diss.streamIDx = 0
	diss.currentOperation = v1pb.HostOperation_Report
	diss.currentVersion = 0

	if diss.store == nil {
		diss.store = store.New(store.Options{
			ReplicationFactor: opts.ReplicationFactor,
		})
	}

	diss.loop = loopFactory.NewLoop(diss)

	diss.timeoutQ = timeout.New(timeout.Options{
		Loop: diss.loop,
	})

	return diss.loop
}

func (d *disseminator) Handle(ctx context.Context, event loops.Event) error {
	switch e := event.(type) {
	case *loops.ConnAdd:
		d.handleAdd(ctx, e)
	case *loops.ReportedHost:
		d.handleReportedHost(e)
	case *loops.ConnCloseStream:
		d.handleCloseStream(e)
	case *loops.Shutdown:
		d.handleShutdown()
	case *timeout.Dissemination:
		d.handleTimeout(e)
	default:
		return fmt.Errorf("unknown disseminator event type: %T", e)
	}

	return nil
}

// handleAdd adds a stream to the namespaced disseminator.
func (d *disseminator) handleAdd(ctx context.Context, add *loops.ConnAdd) {
	streamIDx := d.streamIDx
	d.streamIDx++

	streamLoop := stream.New(ctx, stream.Options{
		IDx:           streamIDx,
		Add:           add,
		NamespaceLoop: d.nsLoop,
		Authorizer:    d.authorizer,
	})

	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		_ = streamLoop.Run(ctx)
	}()

	d.streams[streamIDx] = &streamConn{
		loop:           streamLoop,
		currentState:   nil,
		currentVersion: nil,
	}

	d.handleReportedHost(&loops.ReportedHost{
		Host:      add.InitialHost,
		StreamIDx: streamIDx,
	})
}

// handleCloseStream handles a close stream request.
func (d *disseminator) handleCloseStream(closeStream *loops.ConnCloseStream) {
	stream, ok := d.streams[closeStream.StreamIDx]
	if !ok {
		// Ignore old streams.
		return
	}

	d.store.Delete(closeStream.StreamIDx)
	delete(d.streams, closeStream.StreamIDx)
	stream.loop.Close(new(loops.StreamShutdown))

	d.currentVersion++
	d.currentOperation = v1pb.HostOperation_Lock
	for _, s := range d.streams {
		s.currentState = ptr.Of(v1pb.HostOperation_Lock)
		s.loop.Enqueue(&loops.DisseminateLock{
			Version: d.currentVersion,
		})
	}
}

// handleShutdown handles the shutdown of the streams.
func (d *disseminator) handleShutdown() {
	defer d.wg.Wait()

	for _, stream := range d.streams {
		stream.loop.Close(new(loops.StreamShutdown))
	}

	clear(d.streams)
	d.store.DeleteAll()
	d.timeoutQ.Close()

	loopFactory.CacheLoop(d.loop)
	dissCache.Put(d)
}

func (d *disseminator) handleTimeout(timeout *timeout.Dissemination) {
	version := timeout.Key()

	for idx, stream := range d.streams {
		if stream.currentVersion == nil || *stream.currentVersion < version {
			d.handleCloseStream(&loops.ConnCloseStream{
				StreamIDx: idx,
				Error:     fmt.Errorf("dissemination timeout for version %d", version),
			})
		}
	}
}
