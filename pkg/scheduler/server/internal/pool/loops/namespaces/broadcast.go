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

package namespaces

import (
	"context"
	"fmt"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops"
)

// handleBroadcastjob handles broadcast job events.
func (n *namespaces) handleBroadcastJob(ctx context.Context, event *loops.BroadcastJobEvent) error {
	switch event.Event {
	case schedulerv1pb.BroadcastJobEventType_BROADCAST_JOB_EVENT_PUT:
		n.handleBroadcastPut(ctx, event)

	case schedulerv1pb.BroadcastJobEventType_BROADCAST_JOB_EVENT_DELETE:
		n.handleBroadcastDelete(ctx, event)

	case schedulerv1pb.BroadcastJobEventType_BROADCAST_JOB_EVENT_DROP_ALL:
		n.handleBroadcastDropAll(event)

	default:
		panic("unhandled broadcast job event type")
	}

	return nil
}

func (n *namespaces) handleBroadcastPut(ctx context.Context, put *loops.BroadcastJobEvent) {
	fmt.Printf(">>NAMESPACE HANDLE BROADCAST start PUT: %s %v\n", put.Job.Name, put.Job)
	// TODO: @joshvanl: We currently only support durable actor ID broadcast
	// jobs.
	n.jobStore.AddDurableActorID(put.Job)

	connLoop, ok := n.connections[put.Job.Metadata.Namespace]
	if !ok {
		fmt.Printf(">>NAMESPACE HANDLE BROADCAST done PUT: no connection loop for namespace %s\n", put.Job.Name)
		return
	}

	fmt.Printf(">>NAMESPACE HANDLE BROADCAST done PUT: enqueuing job to connection loop for namespace %s\n", put.Job.Name)
	connLoop.loop.Enqueue(put)
}

func (n *namespaces) handleBroadcastDelete(ctx context.Context, del *loops.BroadcastJobEvent) {
	fmt.Printf(">>NAMESPACE HANDLE BROADCAST start DELETE: %s %v\n", del.Job.Name, del.Job)
	// TODO: @joshvanl: We currently only support durable actor ID broadcast
	// jobs.
	n.jobStore.DeleteDurableActorID(del.Job)

	connLoop, ok := n.connections[del.Job.Metadata.Namespace]
	if !ok {
		fmt.Printf(">>NAMESPACE HANDLE BROADCAST done DELETE: no connection loop for namespace %s/%s\n", del.Job.Name, del.Job.Metadata.GetNamespace())
		return
	}

	fmt.Printf(">>NAMESPACE HANDLE BROADCAST done DELETE: enqueuing job to connection loop for namespace %s/%s \n", del.Job.Name, del.Job.Metadata.GetNamespace())
	connLoop.loop.Enqueue(del)
}

func (n *namespaces) handleBroadcastDropAll(dropAll *loops.BroadcastJobEvent) {
	n.jobStore.ClearDurableActorIDs()

	for _, connLoop := range n.connections {
		// TODO: @joshvanl: run the closes in parallel.
		connLoop.loop.Enqueue(dropAll)
	}

	return
}
