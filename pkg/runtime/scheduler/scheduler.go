/*
Copyright 2024 The Dapr Authors
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

package scheduler

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"

	"github.com/dapr/dapr/pkg/actors"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/runtime/channels"
	"github.com/dapr/dapr/pkg/runtime/scheduler/clients"
	"github.com/dapr/kit/concurrency"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.scheduler")

type Options struct {
	Namespace string
	AppID     string
	Clients   *clients.Clients
	Channels  *channels.Channels
}

// Manager manages connections to multiple schedulers.
type Manager struct {
	clients   *clients.Clients
	namespace string
	appID     string
	actors    actors.ActorRuntime
	channels  *channels.Channels

	startCh chan struct{}
	started atomic.Bool
	wg      sync.WaitGroup
	closeCh chan struct{}
	running atomic.Bool
}

func New(opts Options) (*Manager, error) {
	return &Manager{
		namespace: opts.Namespace,
		appID:     opts.AppID,
		clients:   opts.Clients,
		channels:  opts.Channels,
		startCh:   make(chan struct{}),
		closeCh:   make(chan struct{}),
	}, nil
}

// Run starts watching for job triggers from all scheduler clients.
func (m *Manager) Run(ctx context.Context) error {
	if !m.running.CompareAndSwap(false, true) {
		return errors.New("scheduler manager is already running")
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-m.startCh:
	}

	var entities []string
	if m.actors != nil {
		entities = m.actors.Entities()
	}

	req := &schedulerv1pb.WatchJobsRequest{
		WatchJobRequestType: &schedulerv1pb.WatchJobsRequest_Initial{
			Initial: &schedulerv1pb.WatchJobsRequestInitial{
				AppId:      m.appID,
				Namespace:  m.namespace,
				ActorTypes: entities,
			},
		},
	}

	clients := m.clients.All()
	runners := make([]concurrency.Runner, len(clients))

	for i := 0; i < len(clients); i++ {
		runners[i] = (&connector{
			req:      req,
			client:   clients[i],
			channels: m.channels,
			actors:   m.actors,
		}).run
	}

	return concurrency.NewRunnerManager(runners...).Run(ctx)
}

func (m *Manager) Start(actors actors.ActorRuntime) {
	if m.started.CompareAndSwap(false, true) {
		m.actors = actors
		close(m.startCh)
	}
}
