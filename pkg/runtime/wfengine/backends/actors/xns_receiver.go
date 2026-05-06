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

package actors

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/protobuf/proto"

	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
)

// xnsReceiverImpl implements xns.Receiver by invoking the local Actors
// backend directly. It is the inbound side of the cross-namespace bridge:
// the WorkflowCrossNS gRPC handler hands a forwarded op to the local xns
// actor, which calls this Execute on its reminder fire.
type xnsReceiverImpl struct {
	backend *Actors
}

// NewXNSReceiver constructs the inbound-side xns.Receiver. The returned
// Receiver dispatches each forwarded op to the matching local backend
// method using the original durabletask request payload.
func NewXNSReceiver(b *Actors) *xnsReceiverImpl {
	return &xnsReceiverImpl{backend: b}
}

// Execute implements xns.Receiver. The router on the embedded request is
// stripped before invoking the local backend; otherwise the local call
// would loop back through the cross-namespace branch.
func (r *xnsReceiverImpl) Execute(ctx context.Context, req *internalsv1pb.ForwardOpRequest) (*internalsv1pb.ForwardOpResponse, error) {
	id := api.InstanceID(req.GetInstanceId())

	switch req.GetOperation() {
	case internalsv1pb.WorkflowOpKind_WORKFLOW_OP_TERMINATE,
		internalsv1pb.WorkflowOpKind_WORKFLOW_OP_RAISE,
		internalsv1pb.WorkflowOpKind_WORKFLOW_OP_PAUSE,
		internalsv1pb.WorkflowOpKind_WORKFLOW_OP_RESUME:
		// Event-bearing ops: payload is a HistoryEvent.
		e := new(backend.HistoryEvent)
		if err := proto.Unmarshal(req.GetPayload(), e); err != nil {
			return nil, fmt.Errorf("xns receiver: unmarshal HistoryEvent: %w", err)
		}
		stripRouter(e)
		if err := r.backend.AddNewWorkflowEvent(ctx, id, e); err != nil {
			return nil, err
		}
		return &internalsv1pb.ForwardOpResponse{}, nil

	case internalsv1pb.WorkflowOpKind_WORKFLOW_OP_SCHEDULE:
		// Payload is a CreateWorkflowInstanceRequest (envelope around an
		// ExecutionStarted HistoryEvent).
		creq := new(backend.CreateWorkflowInstanceRequest)
		if err := proto.Unmarshal(req.GetPayload(), creq); err != nil {
			return nil, fmt.Errorf("xns receiver: unmarshal CreateWorkflowInstanceRequest: %w", err)
		}
		if creq.GetStartEvent() != nil {
			stripRouter(creq.GetStartEvent())
		}
		if err := r.backend.CreateWorkflowInstance(ctx, creq.GetStartEvent()); err != nil {
			return nil, err
		}
		return &internalsv1pb.ForwardOpResponse{}, nil

	case internalsv1pb.WorkflowOpKind_WORKFLOW_OP_PURGE:
		preq := new(protos.PurgeInstancesRequest)
		if err := proto.Unmarshal(req.GetPayload(), preq); err != nil {
			return nil, fmt.Errorf("xns receiver: unmarshal PurgeInstancesRequest: %w", err)
		}
		count, err := r.backend.PurgeWorkflowState(ctx, id, nil /* drop router */, preq.GetForce())
		if err != nil {
			return nil, err
		}
		out := &protos.PurgeInstancesResponse{DeletedInstanceCount: int32(count)}
		body, err := proto.Marshal(out)
		if err != nil {
			return nil, err
		}
		return &internalsv1pb.ForwardOpResponse{Payload: body}, nil

	case internalsv1pb.WorkflowOpKind_WORKFLOW_OP_GET:
		meta, err := r.backend.GetWorkflowMetadata(ctx, id, nil /* drop router */)
		if err != nil {
			return nil, err
		}
		body, err := proto.Marshal(meta)
		if err != nil {
			return nil, err
		}
		return &internalsv1pb.ForwardOpResponse{Payload: body}, nil

	case internalsv1pb.WorkflowOpKind_WORKFLOW_OP_RERUN:
		rreq := new(backend.RerunWorkflowFromEventRequest)
		if err := proto.Unmarshal(req.GetPayload(), rreq); err != nil {
			return nil, fmt.Errorf("xns receiver: unmarshal RerunWorkflowFromEventRequest: %w", err)
		}
		rreq.Router = nil
		newID, err := r.backend.RerunWorkflowFromEvent(ctx, rreq)
		if err != nil {
			return nil, err
		}
		out := &protos.RerunWorkflowFromEventResponse{NewInstanceID: string(newID)}
		body, err := proto.Marshal(out)
		if err != nil {
			return nil, err
		}
		return &internalsv1pb.ForwardOpResponse{Payload: body}, nil

	default:
		return nil, fmt.Errorf("xns receiver: unsupported op kind %d", req.GetOperation())
	}
}

func stripRouter(e *backend.HistoryEvent) {
	if e == nil {
		return
	}
	e.Router = nil
}

// compile-time assertion the receiver satisfies the xns.Receiver
// interface; circular import prevented by stating the shape inline.
var _ interface {
	Execute(ctx context.Context, req *internalsv1pb.ForwardOpRequest) (*internalsv1pb.ForwardOpResponse, error)
} = (*xnsReceiverImpl)(nil)

// errReceiverUnknownOp is exported as a sentinel for tests.
var errReceiverUnknownOp = errors.New("xns receiver: unknown operation")
