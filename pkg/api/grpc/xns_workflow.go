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

package grpc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	workflowacl "github.com/dapr/dapr/pkg/acl/workflow"
	"github.com/dapr/dapr/pkg/actors/router"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/xns"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
)

// XNSWorkflowServer implements the WorkflowCrossNS gRPC service. The
// outbound side (a peer daprd in another namespace) calls ForwardOp; this
// server authenticates the SPIFFE peer, dispatches to the local app's xns
// actor in the inbound direction, awaits the result, and returns it.
type XNSWorkflowServer struct {
	internalsv1pb.UnimplementedWorkflowCrossNSServer

	appID        string
	namespace    string
	xnsActorType string
	router       router.Interface
	identity     CallerIdentityExtractor
}

// CallerIdentityExtractor abstracts SPIFFE-from-context for testability.
// In production this is wired to spiffe.FromGRPCContext.
type CallerIdentityExtractor interface {
	Extract(ctx context.Context) (appID, namespace string, err error)
}

func NewXNSWorkflowServer(appID, namespace, xnsActorType string, router router.Interface, identity CallerIdentityExtractor) *XNSWorkflowServer {
	return &XNSWorkflowServer{
		appID:        appID,
		namespace:    namespace,
		xnsActorType: xnsActorType,
		router:       router,
		identity:     identity,
	}
}

// ForwardOp implements WorkflowCrossNSServer.
func (s *XNSWorkflowServer) ForwardOp(ctx context.Context, req *internalsv1pb.ForwardOpRequest) (*internalsv1pb.ForwardOpResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "ForwardOp: nil request")
	}
	if req.GetTargetAppId() != s.appID {
		return nil, status.Errorf(codes.InvalidArgument,
			"ForwardOp: target_app_id %q does not match this sidecar's app %q", req.GetTargetAppId(), s.appID)
	}

	callerAppID, callerNamespace, err := s.identity.Extract(ctx)
	if err != nil {
		return nil, err
	}
	if callerAppID == "" {
		return nil, status.Error(codes.PermissionDenied, "ForwardOp: missing caller identity")
	}

	// Idempotency: the actor itself dedups by forward_id. Compute or trust
	// the supplied forward_id (untrusted callers cannot bypass dedup
	// because the actor uses forward_id only as the actor instance key —
	// it does not influence the executed op).
	if req.GetForwardId() == "" {
		req.ForwardId = computeForwardIDForReceive(req)
	}

	body, err := proto.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("ForwardOp: marshal: %w", err)
	}

	invokeReq := internalsv1pb.
		NewInternalInvokeRequest(xns.ScheduleMethod).
		WithActor(s.xnsActorType, req.GetForwardId()).
		WithData(body).
		WithContentType(invokev1.ProtobufContentType).
		WithMetadata(map[string][]string{"direction": {string(xns.DirectionInbound)}})

	// Stamp the SPIFFE-extracted caller identity onto the invocation so
	// the per-actor checkAccessPolicy sees the right (appID, namespace).
	workflowacl.SetCallerIdentity(invokeReq, callerAppID, callerNamespace)

	resp, err := s.router.Call(ctx, invokeReq)
	if err != nil {
		return nil, err
	}

	out := new(internalsv1pb.ForwardOpResponse)
	if err := proto.Unmarshal(resp.GetMessage().GetData().GetValue(), out); err != nil {
		return nil, fmt.Errorf("ForwardOp: decode response: %w", err)
	}
	return out, nil
}

func computeForwardIDForReceive(req *internalsv1pb.ForwardOpRequest) string {
	h := sha256.New()
	fmt.Fprintf(h, "%d/%s/%s/", req.GetOperation(), req.GetInstanceId(), req.GetTargetAppId())
	h.Write(req.GetPayload())
	return hex.EncodeToString(h.Sum(nil))
}
