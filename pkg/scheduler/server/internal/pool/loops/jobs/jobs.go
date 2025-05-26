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

package jobs

import (
	"context"
	"fmt"

	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops"
	"github.com/dapr/kit/events/loop"
)

type Options struct {
	ConnectionsLoop loop.Interface[loops.Event]
}

type jobs struct {
	connsLoop loop.Interface[loops.Event]
	active    map[string]*internalsv1pb.JobEvent
}

func New(opts Options) loop.Interface[loops.Event] {
	return loop.New(&jobs{
		connsLoop: opts.ConnectionsLoop,
		active:    make(map[string]*internalsv1pb.JobEvent),
	}, 1024)
}

func (j *jobs) Handle(ctx context.Context, event loops.Event) error {
	fmt.Printf(">>Handling Jobs event: %T %v\n", event, event)
	switch event := event.(type) {
	case *loops.JobPut:
		j.handlePut(event)
	case *loops.JobDelete:
		j.handleDelete(event)
	case *loops.JobDropAll:
		j.handleDropAll()
	case *loops.Shutdown:
	default:
		return fmt.Errorf("unknown jobs event type: %T", event)
	}

	return nil
}

func (j *jobs) handlePut(event *loops.JobPut) {
	if event.Job.Metadata.GetTarget().GetBroadcast() == nil {
		return
	}

	j.active[event.Job.GetKey()] = event.Job

	j.connsLoop.Enqueue(&loops.BroadcastAddJob{
		Job: event.Job,
	})
}

func (j *jobs) handleDelete(event *loops.JobDelete) {
	if event.Job.Metadata.GetTarget().GetBroadcast() == nil {
		return
	}

	job, ok := j.active[event.Job.GetKey()]
	if ok {
		delete(j.active, event.Job.GetKey())
		j.connsLoop.Enqueue(&loops.BroadcastDeleteJob{
			Job: job,
		})
	}
}

func (j *jobs) handleDropAll() {
	for k, job := range j.active {
		j.connsLoop.Enqueue(&loops.BroadcastDeleteJob{Job: job})
		delete(j.active, k)
	}
}
