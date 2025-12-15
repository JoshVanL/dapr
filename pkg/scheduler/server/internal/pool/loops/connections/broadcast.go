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
	"fmt"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops"
)

func (c *connections) handleBroadcast(event *loops.BroadcastJobEvent) {
	fmt.Printf(">>CONNECTIONS BROADCAST HANDLING %s\n", event.Event)

	// TODO: @joshvanl: we currently only support durable actor broadcasts
	if event.Job.Metadata.GetTarget().GetBroadcast().GetDurableActorId() == nil {
		panic("TODO: @joshvanl: REMOVE ME! Non-durable actor broadcasts are not yet supported")
		return
	}

	switch event.Event {
	case schedulerv1pb.BroadcastJobEventType_BROADCAST_JOB_EVENT_PUT:
		c.handleBroadcastPut(event)
	case schedulerv1pb.BroadcastJobEventType_BROADCAST_JOB_EVENT_DELETE:
		c.handleBroadcastDelete(event)
	case schedulerv1pb.BroadcastJobEventType_BROADCAST_JOB_EVENT_DROP_ALL:
		c.handleBroadcastDropAll(event)
	}
}

func (c *connections) handleBroadcastPut(put *loops.BroadcastJobEvent) {
	fmt.Printf(">>PUTTING JOB: %s ---- streams=(%v)\n", put.Job.Name, c.streamPool.DurableActorIDs())
	for _, stream := range c.streamPool.DurableActorIDs() {
		fmt.Printf(">>ENQUEUEING PUT JOB: %s TO STREAM: %p\n", put.Job.Name, stream)
		stream.Enqueue(put)
	}
	fmt.Printf(">>DONE PUTTING JOB: %s ---- streams=(%v)\n", put.Job.Name, c.streamPool.DurableActorIDs())
}

func (c *connections) handleBroadcastDelete(del *loops.BroadcastJobEvent) {
	fmt.Printf(">>DELETING JOB: %s ---- streams=(%v)\n", del.Job.Name, c.streamPool.DurableActorIDs())
	for _, stream := range c.streamPool.DurableActorIDs() {
		fmt.Printf(">>ENQUEUEING PUT JOB: %s TO STREAM: %p\n", del.Job.Name, stream)
		stream.Enqueue(del)
	}
	fmt.Printf(">>DONE DELETING JOB: %s ---- streams=(%v)\n", del.Job.Name, c.streamPool.DurableActorIDs())
}

func (c *connections) handleBroadcastDropAll(drop *loops.BroadcastJobEvent) {
	fmt.Printf(">>DROPPING ALL JOBS ---- streams=(%v)\n", c.streamPool.DurableActorIDs())
	for _, stream := range c.streamPool.DurableActorIDs() {
		fmt.Printf(">>ENQUEUEING DROP ALL TO STREAM: %p\n", stream)
		stream.Enqueue(drop)
	}
	fmt.Printf(">>DONE DROPPING ALL JOBS ---- streams=(%v)\n", c.streamPool.DurableActorIDs())
}
