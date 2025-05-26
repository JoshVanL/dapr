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
	"fmt"
	"sync"
	"sync/atomic"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops/connections/store"
	"github.com/dapr/kit/events/loop"
	"github.com/dapr/kit/ptr"
	"github.com/diagridio/go-etcd-cron/api"
)

type trigger struct {
	streamPool *store.Namespace
}

// handleTriggerRequest handles a trigger request for a job.
func (t *trigger) handleTriggerRequest(req *loops.TriggerRequest) {
	streams, ok := t.getStreamLoops(req.Job.GetMetadata())
	if !ok {
		fmt.Printf(">>COULDN'T FIND LOOP FOR JOB: %s\n", req.Job.GetKey())
		req.ResultFn(api.TriggerResponseResult_UNDELIVERABLE)
		return
	}

	var resp atomic.Pointer[api.TriggerResponseResult]
	resp.Store(ptr.Of(api.TriggerResponseResult_SUCCESS))

	var wg sync.WaitGroup
	wg.Add(len(streams))
	for _, loop := range streams {
		loop.Enqueue(&loops.TriggerRequest{
			Job: req.Job,
			ResultFn: func(r api.TriggerResponseResult) {
				if r != api.TriggerResponseResult_SUCCESS {
					resp.Store(&r)
				}
				wg.Done()
			},
		})
	}

	wg.Wait()
	req.ResultFn(*resp.Load())
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
		return nil, false
	default:
		return nil, false
	}
}
