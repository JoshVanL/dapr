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
	"errors"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	anypb "google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"

	workflowacl "github.com/dapr/dapr/pkg/acl/workflow"
	actorapi "github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
	wfaclapi "github.com/dapr/dapr/pkg/apis/workflowaccesspolicy/v1alpha1"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
)

// wfaclapiSchedule is the schedule operation alias used for the result
// authorization shorthand below.
var wfaclapiSchedule = wfaclapi.WorkflowOperationSchedule

// xnsOperationFromMethod returns the WorkflowOperation associated with the
// inner method dispatched across namespaces.
func xnsOperationFromMethod(method string, payload []byte) (wfaclapi.WorkflowOperation, error) {
	switch method {
	case todo.CreateWorkflowInstanceMethod, todo.ExecuteActivityMethod:
		return wfaclapi.WorkflowOperationSchedule, nil
	default:
		return "", errors.New("unsupported cross-ns method " + method)
	}
}

// xnsOpName extracts the workflow or activity name from the inner payload
// so policy evaluation can match per-name rules.
func xnsOpName(opType workflowacl.OperationType, method string, payload []byte) (string, error) {
	switch method {
	case todo.CreateWorkflowInstanceMethod:
		return workflowacl.WorkflowNameFromCreateRequest(payload)
	case todo.ExecuteActivityMethod:
		return workflowacl.ActivityNameFromExecute(method, payload)
	default:
		return "", errors.New("unsupported cross-ns method " + method)
	}
}

// CallWorkflowCrossNamespace is the target-side handler for cross-namespace
// workflow/activity dispatch. Steps:
//
//  1. Extracts the caller's (namespace, appID) from the SPIFFE ID on the
//     mTLS connection. This is the security boundary; the Source fields in
//     the request are informational only.
//  2. Default-deny when no WorkflowAccessPolicy is loaded — cross-namespace
//     calls require an explicit policy ingress rule. Stricter than the
//     same-namespace default (which allows nil policies through) because
//     cross-namespace is a security boundary that must not be implicitly
//     open.
//  3. Evaluates the policy against the parsed actor type + method + payload.
//  4. Creates a local idempotent reminder named after the dispatch key. A
//     duplicate Create is a no-op, so caller retries land on the same
//     reminder and the work executes exactly once.
//  5. Returns ACK. Durability is handed off the moment the reminder is
//     persisted.
func (a *api) CallWorkflowCrossNamespace(ctx context.Context, req *internalv1pb.CrossNSDispatchRequest) (*internalv1pb.CrossNSAck, error) {
	callerAppID, callerNamespace, err := a.extractCallerIdentity(ctx)
	if err != nil {
		return nil, err
	}

	policies := a.workflowAccessPolicies.Load()
	if policies == nil {
		a.logger.Warnf("Cross-namespace workflow call from '%s/%s' denied: no policy configured on target", callerNamespace, callerAppID)
		return nil, status.Error(codes.PermissionDenied, workflowACLDeniedMsg)
	}

	opType, _, _, ok := workflowacl.SplitActorType(req.GetActorType())
	if !ok {
		return nil, status.Errorf(codes.InvalidArgument, "invalid actor_type %q", req.GetActorType())
	}
	operation, err := xnsOperationFromMethod(req.GetMethod(), req.GetPayload())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "deriving operation: %v", err)
	}
	opName, err := xnsOpName(opType, req.GetMethod(), req.GetPayload())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "deriving op name: %v", err)
	}
	if !policies.Evaluate(callerAppID, callerNamespace, opType, operation, opName) {
		a.logger.Warnf("Cross-namespace workflow call from '%s/%s' denied for %s operation '%s' on '%s'", callerNamespace, callerAppID, opType, operation, opName)
		diag.DefaultMonitoring.WorkflowACLActionDenied(callerAppID, string(opType), string(operation))
		return nil, status.Error(codes.PermissionDenied, workflowACLDeniedMsg)
	}
	diag.DefaultMonitoring.WorkflowACLActionAllowed(callerAppID, string(opType), string(operation))

	reminders, err := a.ActorReminders(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to access reminders: %v", err)
	}

	data, perr := anypb.New(req)
	if perr != nil {
		return nil, status.Errorf(codes.Internal, "failed to encode reminder payload: %v", perr)
	}

	reminderName := common.ReminderPrefixXNSExec + req.GetIdempotencyKey()
	createErr := reminders.Create(ctx, &actorapi.CreateReminderRequest{
		ActorType: req.GetActorType(),
		ActorID:   req.GetActorId(),
		Name:      reminderName,
		Data:      data,
		DueTime:   time.Now().UTC().Format(time.RFC3339),
		FailurePolicy: &commonv1pb.JobFailurePolicy{
			Policy: &commonv1pb.JobFailurePolicy_Constant{
				Constant: &commonv1pb.JobFailurePolicyConstant{
					Interval:   durationpb.New(time.Second),
					MaxRetries: nil,
				},
			},
		},
	})
	if createErr != nil {
		// AlreadyExists is success for idempotency: the work is durable.
		if status.Code(createErr) == codes.AlreadyExists {
			return &internalv1pb.CrossNSAck{}, nil
		}
		return nil, status.Errorf(codes.Internal, "failed to schedule cross-namespace exec reminder: %v", createErr)
	}

	return &internalv1pb.CrossNSAck{}, nil
}

// DeliverWorkflowResultCrossNamespace is the parent-side handler for
// cross-namespace completion callbacks.
//
//  1. SPIFFE identity extraction.
//  2. Default-deny when no policy is loaded.
//  3. Policy check: the parent's policy must authorize the responding child
//     app — result delivery is a policy-gated operation.
//  4. Create a local idempotent xns-result-in reminder that carries the
//     event into the parent orchestrator's inbox. The firing handler does
//     the executionId comparison against the parent's currently-loaded
//     state, so stale-result-drop happens at reminder fire (not at gRPC
//     ingress, where the parent's state may not be loaded yet).
func (a *api) DeliverWorkflowResultCrossNamespace(ctx context.Context, req *internalv1pb.CrossNSResultRequest) (*internalv1pb.CrossNSAck, error) {
	callerAppID, callerNamespace, err := a.extractCallerIdentity(ctx)
	if err != nil {
		return nil, err
	}

	policies := a.workflowAccessPolicies.Load()
	if policies == nil {
		a.logger.Warnf("Cross-namespace workflow result from '%s/%s' denied: no policy configured on parent", callerNamespace, callerAppID)
		return nil, status.Error(codes.PermissionDenied, workflowACLDeniedMsg)
	}
	// The caller must be allowed to send results back; require at least an
	// allow-rule match against any workflow op-name. Use the wildcard
	// pattern "*" to test trustedness of the caller for the workflow op
	// type. If no rule allows the caller for any workflow op the result is
	// rejected.
	if !policies.Evaluate(callerAppID, callerNamespace, workflowacl.OperationTypeWorkflow, wfaclapiSchedule, "*") {
		a.logger.Warnf("Cross-namespace workflow result from '%s/%s' denied: caller not authorized", callerNamespace, callerAppID)
		diag.DefaultMonitoring.WorkflowACLActionDenied(callerAppID, "xns-result", "deliver")
		return nil, status.Error(codes.PermissionDenied, workflowACLDeniedMsg)
	}
	diag.DefaultMonitoring.WorkflowACLActionAllowed(callerAppID, "xns-result", "deliver")

	reminders, err := a.ActorReminders(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to access reminders: %v", err)
	}

	data, perr := anypb.New(req)
	if perr != nil {
		return nil, status.Errorf(codes.Internal, "failed to encode reminder payload: %v", perr)
	}

	// Parent reminder lives on the local workflow actor; the actor ID is
	// the parent instance ID. The reminder name encodes the idempotency
	// key so caller retries collapse to a single delivery.
	actorType := "dapr.internal." + a.Namespace() + "." + a.AppID() + ".workflow"
	reminderName := common.ReminderPrefixXNSResultIn + req.GetIdempotencyKey()
	createErr := reminders.Create(ctx, &actorapi.CreateReminderRequest{
		ActorType: actorType,
		ActorID:   req.GetParentInstanceId(),
		Name:      reminderName,
		Data:      data,
		DueTime:   time.Now().UTC().Format(time.RFC3339),
		FailurePolicy: &commonv1pb.JobFailurePolicy{
			Policy: &commonv1pb.JobFailurePolicy_Constant{
				Constant: &commonv1pb.JobFailurePolicyConstant{
					Interval:   durationpb.New(time.Second),
					MaxRetries: nil,
				},
			},
		},
	})
	if createErr != nil {
		if status.Code(createErr) == codes.AlreadyExists {
			return &internalv1pb.CrossNSAck{}, nil
		}
		return nil, status.Errorf(codes.Internal, "failed to schedule cross-namespace result reminder: %v", createErr)
	}

	return &internalv1pb.CrossNSAck{}, nil
}
