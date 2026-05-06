/*
Copyright 2026 The Dapr Authors
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

package xns

import (
	"context"
	"sync"

	"github.com/dapr/dapr/pkg/actors"
	"github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/reminders"
	"github.com/dapr/dapr/pkg/actors/targets"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/common/lock"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
)

// Forwarder performs the outbound service-invocation step of the bridge:
// it sends the ForwardOp to the target namespace's daprd and returns the
// response. It is plugged in by the runtime; tests pass a fake.
type Forwarder interface {
	Forward(ctx context.Context, req *internalsv1pb.ForwardOpRequest) (*internalsv1pb.ForwardOpResponse, error)
}

// Receiver executes a ForwardOp locally on the receiving sidecar by
// invoking the local workflow engine. Plugged in by the runtime.
type Receiver interface {
	Execute(ctx context.Context, req *internalsv1pb.ForwardOpRequest) (*internalsv1pb.ForwardOpResponse, error)
}

var xnsCache = &sync.Pool{
	New: func() any {
		return &xns{lock: lock.New()}
	},
}

type Options struct {
	AppID     string
	Namespace string
	ActorType string

	Actors actors.Interface

	// Forwarder is invoked when an instance is in outbound state and its
	// reminder fires. Required.
	Forwarder Forwarder
	// Receiver is invoked when an instance is in inbound state and its
	// reminder fires. Required.
	Receiver Receiver
}

type factory struct {
	appID     string
	namespace string
	actorType string

	reminders reminders.Interface

	forwarder Forwarder
	receiver  Receiver
}

func New(ctx context.Context, opts Options) (targets.Factory, error) {
	rem, err := opts.Actors.Reminders(ctx)
	if err != nil {
		return nil, err
	}
	return &factory{
		appID:     opts.AppID,
		namespace: opts.Namespace,
		actorType: opts.ActorType,
		reminders: rem,
		forwarder: opts.Forwarder,
		receiver:  opts.Receiver,
	}, nil
}

func (f *factory) GetOrCreate(actorID string) targets.Interface {
	x := xnsCache.Get().(*xns)
	x.factory = f
	x.actorID = actorID
	return x
}

func (f *factory) HaltAll(context.Context) error { return nil }
func (f *factory) HaltNonHosted(context.Context, func(*api.LookupActorRequest) bool) error {
	return nil
}
func (f *factory) Exists(string) bool { return false }
func (f *factory) Len() int           { return 0 }
