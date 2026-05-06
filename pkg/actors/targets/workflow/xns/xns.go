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

// Package xns implements the cross-namespace workflow bridge actor. A single
// per-app actor type (`dapr.internal.<ns>.<appID>.workflow.xns`) hosts both
// ends of the bridge: outbound instances live on the source app's sidecar
// and drive a service invocation to the target namespace's daprd; inbound
// instances live on the target app's sidecar and drive a local workflow
// engine call. The two sides are placed naturally on different sidecars by
// virtue of which sidecar created the instance. Each instance's actorID is
// the deterministic `forward_id`, giving idempotent retries.
//
// The actor does not persist any state of its own. Durability comes
// entirely from the actor reminder, whose payload carries the
// ForwardOpRequest. Direction is encoded in the reminder name
// (xns-out vs xns-in) so InvokeReminder can route without unmarshalling.
//
// Files in this package:
//   - xns.go: base actor struct + lifecycle stubs
//   - invoke.go: InvokeMethod / InvokeReminder + reminder helpers
//   - outbound.go: outbound action (calls Forwarder)
//   - inbound.go: inbound action (calls Receiver)
package xns

import (
	"context"
	"errors"

	actorapi "github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/common/lock"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.actors.targets.xns")

const (
	// ScheduleMethod is invoked by local code (outbound side: the actors
	// backend; inbound side: the WorkflowCrossNS gRPC handler) to enqueue
	// a forward op. Idempotent at the actor level by actorID == forward_id.
	ScheduleMethod = "Schedule"

	// reminderOutbound and reminderInbound encode the direction of the
	// bridged call in the reminder's name. The actor reads the reminder
	// name in InvokeReminder to decide which action to dispatch, with no
	// extra payload framing.
	reminderOutbound = "xns-out"
	reminderInbound  = "xns-in"

	// metadataDirection carries the direction on the inbound Schedule
	// call so InvokeMethod knows which reminder name to use.
	metadataDirection = "direction"
)

// Direction encodes which side of the bridge an instance owns. It is
// only ever in-flight metadata and never persisted: the durable
// representation is the reminder name.
type Direction string

const (
	DirectionOutbound Direction = "outbound"
	DirectionInbound  Direction = "inbound"
)

func (d Direction) reminderName() (string, error) {
	switch d {
	case DirectionOutbound:
		return reminderOutbound, nil
	case DirectionInbound:
		return reminderInbound, nil
	default:
		return "", errors.New("xns: invalid direction")
	}
}

// xns is one bridge instance. The actorID is the deterministic forward_id;
// the reminder carries the input op.
type xns struct {
	*factory
	actorID string
	lock    *lock.Lock
}

func (x *xns) Type() string { return x.actorType }
func (x *xns) ID() string   { return x.actorID }
func (x *xns) Key() string  { return x.actorType + actorapi.DaprSeparator + x.actorID }

func (x *xns) Deactivate(context.Context) error {
	xnsCache.Put(x)
	return nil
}

func (x *xns) InvokeStream(ctx context.Context, req *internalsv1pb.InternalInvokeRequest, stream func(*internalsv1pb.InternalInvokeResponse) (bool, error)) error {
	return errors.New("invoke stream is not implemented")
}

func (x *xns) InvokeTimer(ctx context.Context, _ *actorapi.Reminder) error {
	return errors.New("timers are not implemented")
}
