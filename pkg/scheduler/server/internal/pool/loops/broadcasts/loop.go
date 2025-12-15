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

package broadcasts

import (
	"context"
	"fmt"

	"github.com/diagridio/go-etcd-cron/api"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/serialize"
	"github.com/dapr/kit/events/loop"
)

type broadcastEvent any

type handler struct {
	nsLoop loop.Interface[loops.Event]
}

func (h *handler) Handle(ctx context.Context, event broadcastEvent) error {
	switch e := event.(type) {
	case *api.InformerEvent:
		return h.handleBroadcast(ctx, e)
	case *loops.Shutdown:
		return nil
	default:
		panic(fmt.Sprintf("unknown event type: %T", e))
	}
}

func (h *handler) handleBroadcast(ctx context.Context, event *api.InformerEvent) error {
	switch e := event.GetEvent().(type) {
	case *api.InformerEvent_Put:
		return h.handlePut(e.Put)

	case *api.InformerEvent_Delete:
		return h.handleDelete(e.Delete)

	case *api.InformerEvent_DropAll:
		h.handleDropAll()
		return nil

	default:
		panic(fmt.Sprintf("unknown connections event type: %T", e))
	}
}

func (h *handler) handlePut(job *api.InformerEventJob) error {
	fmt.Printf(">>BROADCAST GOT PUT %s\n", job.Name)
	return h.handleEvent(job,
		schedulerv1pb.BroadcastJobEventType_BROADCAST_JOB_EVENT_PUT)
}

func (h *handler) handleDelete(job *api.InformerEventJob) error {
	fmt.Printf(">>BROADCAST GOT DELETE %s\n", job.Name)
	return h.handleEvent(job,
		schedulerv1pb.BroadcastJobEventType_BROADCAST_JOB_EVENT_DELETE)
}

func (h *handler) handleDropAll() {
	h.nsLoop.Enqueue(&loops.BroadcastJobEvent{
		Event: schedulerv1pb.BroadcastJobEventType_BROADCAST_JOB_EVENT_DROP_ALL,
		Job: &loops.BroadcastJob{
			Metadata: &schedulerv1pb.JobMetadata{
				Target: &schedulerv1pb.JobTargetMetadata{
					Type: &schedulerv1pb.JobTargetMetadata_Broadcast{
						Broadcast: new(schedulerv1pb.TargetBroadcast),
					},
				},
			},
		},
	})
}

func (h *handler) handleEvent(job *api.InformerEventJob, event schedulerv1pb.BroadcastJobEventType) error {
	var meta schedulerv1pb.JobMetadata
	if err := job.GetMetadata().UnmarshalTo(&meta); err != nil {
		return fmt.Errorf("error unmarshalling job metadata: %w", err)
	}

	if meta.GetTarget().GetBroadcast() == nil {
		return nil
	}

	jobName, err := serialize.JobNameFromKey(job.Name)
	if err != nil {
		return err
	}

	h.nsLoop.Enqueue(&loops.BroadcastJobEvent{
		Event: event,
		Job: &loops.BroadcastJob{
			Name:     jobName,
			Metadata: &meta,
			Payload:  job.Payload,
		},
	})

	return nil
}
