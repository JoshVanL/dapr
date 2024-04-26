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

package clients

import (
	"context"
	"fmt"
	"sync/atomic"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/scheduler/client"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.scheduler.clients")

// Options contains the configuration options for the Scheduler clients.
type Options struct {
	Addresses []string
	Security  security.Handler
}

// Clients builds Scheduler clients and provides those clients in a round-robin
// fashion.
type Clients struct {
	clients     []*client.Client
	lastUsedIdx atomic.Uint64
}

func New(ctx context.Context, opts Options) (*Clients, error) {
	clients := make([]*client.Client, len(opts.Addresses))
	for i, address := range opts.Addresses {
		log.Debugf("Attempting to connect to Scheduler at address: %s", address)
		conn, cli, err := client.New(ctx, address, opts.Security)
		if err != nil {
			return nil, fmt.Errorf("Scheduler client not initialized for address: %s", address)
		}

		log.Infof("Scheduler client initialized for address: %s", address)

		clients[i] = &client.Client{
			Conn:      conn,
			Scheduler: cli,
			Address:   address,
			Security:  opts.Security,
		}
	}

	return &Clients{
		clients: clients,
	}, nil
}

// Next returns the next client in a round-robin manner.
func (c *Clients) Next() schedulerv1pb.SchedulerClient {
	// Check if there is only one client available
	if len(c.clients) == 1 {
		return c.clients[0].Scheduler
	}

	return c.clients[int(c.lastUsedIdx.Add(1))%len(c.clients)].Scheduler
}

func (c *Clients) All() []*client.Client {
	return c.clients
}
