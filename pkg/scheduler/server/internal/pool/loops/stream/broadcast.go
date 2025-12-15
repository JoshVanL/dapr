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

package stream

import (
	"context"
	"fmt"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops"
	"google.golang.org/protobuf/types/known/anypb"
)

func (s *stream) handleBroadcast(ctx context.Context, event *loops.BroadcastJobEvent) error {
	payloadP, err := anypb.New(&schedulerv1pb.BroadcastJobDataWrapper{
		Type: event.Event,
		Data: event.Job.Payload,
	})
	if err != nil {
		return err
	}

	s.triggerIDx++
	job := &schedulerv1pb.WatchJobsResponse{
		Name:     event.Job.Name,
		Id:       s.triggerIDx,
		Metadata: event.Job.Metadata,
		Data:     payloadP,
	}

	fmt.Printf(">>STREAM SENDING BROADCAST JOB %s/%s: %+v\n", s.ns, s.appID, job)

	if err := s.channel.Send(job); err != nil {
		log.Warnf("Error sending broadcast job to stream %s/%s: %s", s.ns, s.appID, err)
		s.nsLoop.Enqueue(&loops.ConnCloseStream{
			StreamIDx: s.idx,
			Namespace: s.ns,
		})
	}

	return nil
}
