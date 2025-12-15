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

	"github.com/diagridio/go-etcd-cron/api"

	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops"
	"github.com/dapr/kit/concurrency"
	"github.com/dapr/kit/events/loop"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.scheduler.server.pool.loops.broadcasts")

type Options struct {
	NamespaceLoop loop.Interface[loops.Event]
	ConsumerSink  <-chan *api.InformerEvent
}

// Broadcasts is the control loop that manages the lifecycle of broadcast jobs.
type Broadcasts struct {
	loop loop.Interface[broadcastEvent]

	ch <-chan *api.InformerEvent
}

func New(opts Options) *Broadcasts {
	return &Broadcasts{
		ch: opts.ConsumerSink,
		loop: loop.New[broadcastEvent](1024).NewLoop(&handler{
			nsLoop: opts.NamespaceLoop,
		}),
	}
}

func (b *Broadcasts) Run(ctx context.Context) error {
	return concurrency.NewRunnerManager(
		b.loop.Run,
		func(ctx context.Context) error {
			// Loop forever until the channel is closed. Don't respect context
			// cancellation here as we need to drain the consumer sink completely
			// till close. The closed loop below will drain out the remaining events.
			for {
				event, ok := <-b.ch
				if !ok {
					b.loop.Close(new(loops.Shutdown))
					return nil
				}

				b.loop.Enqueue(event)
			}
		},
	).Run(ctx)
}
