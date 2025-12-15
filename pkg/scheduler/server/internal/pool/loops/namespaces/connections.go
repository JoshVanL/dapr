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
	"errors"
	"fmt"

	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops/connections"
	"github.com/dapr/kit/events/loop"
	"github.com/diagridio/go-etcd-cron/api"
)

type connectionLoop struct {
	streamsN uint64
	loop     loop.Interface[loops.Event]
}

// handleAdd adds a connection to the pool for a given namespace/appID.
func (n *namespaces) handleAdd(ctx context.Context, add *loops.ConnAdd) error {
	connLoop, err := n.getOrCreateNamespaceConnections(ctx, add.Request.GetNamespace())
	if err != nil {
		return err
	}

	connLoop.streamsN++
	add.DurableActorIDs = n.jobStore.GetDurableActorIDs(add.Request.Namespace)
	connLoop.loop.Enqueue(add)

	return nil
}

// handleCloseStream handles a close stream request.
func (n *namespaces) handleCloseStream(closeStream *loops.ConnCloseStream) error {
	connLoop, ok := n.connections[closeStream.Namespace]
	if !ok {
		return nil
	}

	connLoop.streamsN--
	connLoop.loop.Enqueue(closeStream)

	// Close connections loop if there are no streams connected and no
	// broadcast jobs.
	if connLoop.streamsN == 0 {
		fmt.Printf(">>NAMESPACES << Closing connections loop for namespace %s\n", closeStream.Namespace)
		delete(n.connections, closeStream.Namespace)
		connLoop.loop.Close(new(loops.Shutdown))
	}

	return nil
}

// handleTriggerRequest handles a trigger request for a job.
func (n *namespaces) handleTriggerRequest(req *loops.TriggerRequest) error {
	loop, ok := n.connections[req.Job.GetMetadata().GetNamespace()]
	if !ok {
		req.ResultFn(api.TriggerResponseResult_UNDELIVERABLE)
		return nil
	}

	loop.loop.Enqueue(req)

	return nil
}

func (n *namespaces) getOrCreateNamespaceConnections(ctx context.Context, ns string) (*connectionLoop, error) {
	connLoop, ok := n.connections[ns]
	if ok {
		return connLoop, nil
	}

	loop := connections.New(connections.Options{
		Cron:          n.cron,
		NamespaceLoop: n.loop,
	})

	n.wg.Add(1)
	go func() {
		defer n.wg.Done()
		err := loop.Run(ctx)
		if err != nil && !errors.Is(err, context.Canceled) {
			log.Errorf("Error running stream loop: %v", err)
			n.cancelPool(err)
		}
	}()

	connLoop = &connectionLoop{
		loop:     loop,
		streamsN: 0,
	}

	n.connections[ns] = connLoop

	return connLoop, nil
}
