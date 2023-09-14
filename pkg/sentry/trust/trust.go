/*
Copyright 2023 The Dapr Authors
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

package trust

import (
	"context"
	"errors"
	"sync/atomic"

	"github.com/dapr/dapr/pkg/concurrency"
	"github.com/dapr/dapr/pkg/sentry/trust/internal"
	"github.com/dapr/dapr/pkg/sentry/trust/options"
)

// TrustAnchors returns the current trust anchors. Blocks until the initial
// trust anchors are available or until the provided context is canceled.
type TrustAnchors internal.TrustAnchors

func (t *TrustAnchors) PEMBytes() []byte {
	return (*internal.TrustAnchors)(t).PEM()
}

// TrustAnchorsChannel returns a channel that will be notified when the trust
// anchors are initalized and when the trust anchors are updated. The latest trust anchors
// will be sent on the channel.
type TrustAnchorsChannel internal.TrustAnchorsChannel

// Manager is the interface for a trust manager which is responsible for
// returning and reporting the trust anchors of the dapr cluster.
type Manager interface {
	// Run starts the trust manager. Blocks until the initial trust anchors are
	// available.
	Run(context.Context) error

	// Latest returns the latest trust anchors. Blocks until the initial trust
	// anchors are available.
	Latest(context.Context) (*TrustAnchors, error)

	// Subscribe returns a channel that will be notified when the initial trust
	// anchors become available and when the trust anchors are updated.
	Subscribe() TrustAnchorsChannel
}

type manager struct {
	source  internal.Source
	dist    internal.Distributor
	running atomic.Bool
	closeCh chan struct{}
}

func New(opts options.Options) (Manager, error) {
	source, err := opts.BuilderSource.Build()
	if err != nil {
		return nil, err
	}

	distributor, err := opts.BuilderDistributor.Build(source)
	if err != nil {
		return nil, err
	}

	return &manager{
		source:  source,
		dist:    distributor,
		closeCh: make(chan struct{}),
	}, nil
}

func (m *manager) Run(ctx context.Context) error {
	if !m.running.CompareAndSwap(false, true) {
		return errors.New("trust manager is already running")
	}

	return concurrency.NewRunnerManager(
		m.source.Run,
		concurrency.Runner(m.dist),
	).Run(ctx)
}

func (m *manager) Latest(ctx context.Context) (*TrustAnchors, error) {
	latest, err := m.source.Latest(ctx)
	return (*TrustAnchors)(latest), err
}

func (m *manager) Subscribe() TrustAnchorsChannel {
	return (TrustAnchorsChannel)(m.source.Subscribe())
}
