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

package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	workflowacl "github.com/dapr/dapr/pkg/acl/workflow"
	actorapi "github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
	xnscommon "github.com/dapr/dapr/pkg/actors/targets/workflow/common/xns"
	wfaclapi "github.com/dapr/dapr/pkg/apis/workflowaccesspolicy/v1alpha1"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
)

// Retry budget constants are defined in xnscommon. Local aliases are kept
// for readability at call sites in this file.
const (
	xnsRetryInterval = xnscommon.RetryInterval
	xnsMaxRetries    = xnscommon.MaxRetries
)

// xnsFailurePolicy is the bounded failure policy used by cross-namespace
// dispatch and result reminders. Constant interval, capped retry count.
func xnsFailurePolicy() *commonv1pb.JobFailurePolicy {
	max := xnsMaxRetries
	return &commonv1pb.JobFailurePolicy{
		Policy: &commonv1pb.JobFailurePolicy_Constant{
			Constant: &commonv1pb.JobFailurePolicyConstant{
				Interval:   durationpb.New(xnsRetryInterval),
				MaxRetries: &max,
			},
		},
	}
}

// createXNSReminder schedules a cross-namespace bridge reminder with the
// bounded failure policy. Wraps actorapi.CreateReminderRequest construction
// so all xns-prefix reminders share the same retry and durability shape.
func (o *orchestrator) createXNSReminder(ctx context.Context, name string, payload proto.Message) error {
	data, err := anypb.New(payload)
	if err != nil {
		return err
	}
	return common.CreateReminderWithRetry(ctx, o.reminders, &actorapi.CreateReminderRequest{
		ActorType:     o.actorType,
		ActorID:       o.actorID,
		Name:          name,
		Data:          data,
		DueTime:       time.Now().UTC().Format(time.RFC3339),
		FailurePolicy: xnsFailurePolicy(),
	})
}

// Re-exports of the shared cross-namespace primitives. Kept here so
// existing call sites in this package don't need to drag the xnscommon
// import into every file that already deals in orchestrator-only logic.
type XNSHop = xnscommon.Hop

const (
	XNSHopDispatch = xnscommon.HopDispatch
	XNSHopResult   = xnscommon.HopResult
)

// XNSDispatcher is re-exported from common/xns; concrete implementations
// live in pkg/runtime/wfengine/xns.
type XNSDispatcher = xnscommon.Dispatcher

// RoutingKind classifies how a scheduling/history event should be routed.
// It is the single enumeration consulted by the scheduling code paths so
// that new routing targets (for example federated backends) can be added
// by extending this enum rather than by layering more if-statements at
// every call site.
type RoutingKind int

const (
	// RoutingLocal: same namespace and same app as this sidecar; the call
	// goes through the actor router to a local actor.
	RoutingLocal RoutingKind = iota

	// RoutingCrossApp: same namespace, different app on the same placement
	// cluster. Routed via the actor router which finds the remote actor
	// through placement.
	RoutingCrossApp

	// RoutingCrossNS: different namespace. Placement is namespace-scoped so
	// the call is bridged via service invocation with a durable local
	// reminder on each side.
	RoutingCrossNS
)

// classifyRouting inspects a scheduling event's TaskRouter and returns the
// routing kind for this sidecar. Callers switch on the result. Presence
// for optional router fields is detected via nil checks on the pointer —
// the proto3 generated getters return "" for unset values, so the pointer
// is the only way to distinguish "unset" from "deliberately empty".
func (o *orchestrator) classifyRouting(r *protos.TaskRouter) RoutingKind {
	if r == nil {
		return RoutingLocal
	}
	switch {
	case r.TargetAppNamespace != nil && r.GetTargetAppNamespace() != o.actorTypeBuilder.Namespace():
		return RoutingCrossNS
	case r.TargetAppID != nil && r.GetTargetAppID() != o.appID:
		return RoutingCrossApp
	default:
		return RoutingLocal
	}
}

// DeterministicXNSKey is a thin alias for the shared key derivation in
// common/xns. Kept here for orchestrator call sites.
func DeterministicXNSKey(sourceNs, sourceAppID, parentOrchID, parentExecID, childInstanceID, childExecID string, taskID int32, hop XNSHop) string {
	return xnscommon.DeterministicKey(sourceNs, sourceAppID, parentOrchID, parentExecID, childInstanceID, childExecID, taskID, hop)
}

// dispatchCrossNS creates a durable caller-side dispatch reminder whose data
// is a serialized CrossNSDispatchRequest. The reminder fires on this
// orchestrator actor; its handler performs the service-invocation hop to
// the target sidecar. The reminder name is the deterministic idempotency
// key so that replays of an identical hop (e.g. after a caller sidecar
// restart) find the existing reminder and no-op at creation time.
//
// invocationMetadata is forwarded onto the inner actor invocation on the
// receiving side. The receiving handler restricts which keys may flow
// through to prevent a hostile peer from smuggling arbitrary metadata into
// a local actor call. Pass nil when the dispatched method does not consume
// metadata.
func (o *orchestrator) dispatchCrossNS(
	ctx context.Context,
	targetNamespace, targetAppID string,
	targetActorType, targetActorID string,
	method string,
	innerPayload []byte,
	invocationMetadata map[string]*internalsv1pb.ListStringValue,
	parentExecID, childInstanceID, childExecID string,
	taskID int32,
) error {
	key := DeterministicXNSKey(
		o.actorTypeBuilder.Namespace(), o.appID,
		o.actorID, parentExecID,
		childInstanceID, childExecID,
		taskID, XNSHopDispatch,
	)

	req := &internalsv1pb.CrossNSDispatchRequest{
		IdempotencyKey: key,
		TargetAppId:    targetAppID,
		ActorType:      targetActorType,
		ActorId:        targetActorID,
		Method:         method,
		Payload:        innerPayload,
		Source: &internalsv1pb.CrossNSSource{
			Namespace:      o.actorTypeBuilder.Namespace(),
			AppId:          o.appID,
			OrchestratorId: o.actorID,
			ExecutionId:    parentExecID,
			TaskId:         taskID,
		},
		// Stamp the deadline so the reminder handler can detect a budget
		// exhaustion and synthesize a child-failure event rather than
		// silently dropping the dispatch when the scheduler stops firing.
		CallerDeadlineUnixNano: xnscommon.CallerDeadline(time.Now()),
		InvocationMetadata:     invocationMetadata,
	}

	reminderName := common.ReminderPrefixXNSDispatch + key
	if err := o.createXNSReminder(ctx, reminderName, req); err != nil {
		return fmt.Errorf("failed to create cross-ns dispatch reminder: %w", err)
	}
	log.Debugf("Workflow actor '%s': scheduled cross-ns dispatch to '%s/%s' method=%s key=%s", o.actorID, targetNamespace, targetAppID, method, key)
	return nil
}

// handleXNSDispatchReminder fires when a caller-side xns-dispatch reminder is
// due. It unmarshals the CrossNSDispatchRequest, performs the SI call to the
// target sidecar, and on success / terminal error deletes the reminder. On
// transient errors it returns non-nil so the reminder's infinite-retry
// failure policy retries the hop on the next tick.
func (o *orchestrator) handleXNSDispatchReminder(ctx context.Context, reminder *actorapi.Reminder) error {
	if o.xnsDispatcher == nil {
		log.Errorf("Workflow actor '%s': cross-ns dispatch reminder fired but no dispatcher is wired; this reminder will retry indefinitely", o.actorID)
		return errors.New("cross-namespace dispatcher not configured")
	}

	var req internalsv1pb.CrossNSDispatchRequest
	if reminder.Data == nil {
		return o.deleteXNSReminder(ctx, reminder.Name)
	}
	if err := proto.Unmarshal(reminder.Data.GetValue(), &req); err != nil {
		log.Errorf("Workflow actor '%s': cross-ns dispatch reminder '%s' has malformed payload: %v", o.actorID, reminder.Name, err)
		return o.deleteXNSReminder(ctx, reminder.Name)
	}

	// Deadline check: if the caller's retry budget is exhausted, surface
	// a synthetic failure event to the parent orchestrator so the
	// workflow doesn't hang waiting on a dispatch that will never
	// succeed. The reminder is then deleted and the budget exhaustion
	// metric is emitted.
	if dl := req.GetCallerDeadlineUnixNano(); dl > 0 && time.Now().UnixNano() >= dl {
		log.Warnf("Workflow actor '%s': cross-ns dispatch reminder '%s' exceeded retry budget; synthesizing failure", o.actorID, reminder.Name)
		diag.DefaultWorkflowMonitoring.WorkflowOperationEvent(ctx, diag.XNSDispatch, diag.StatusFailed, 0)
		if ferr := o.failXNSDispatch(ctx, &req, "CrossNamespaceDispatchTimeout"); ferr != nil {
			return fmt.Errorf("failed to signal xns dispatch timeout: %w", ferr)
		}
		return o.deleteXNSReminder(ctx, reminder.Name)
	}

	// Source.Namespace identifies the *caller* (informational), not the
	// destination — extract the target namespace from the rendered actor
	// type ("dapr.internal.<ns>.<app>.workflow") so we route to the right
	// sidecar.
	_, targetNs, _, ok := workflowacl.SplitActorType(req.GetActorType())
	if !ok {
		log.Errorf("Workflow actor '%s': cross-ns dispatch reminder '%s' has unparseable actor_type %q", o.actorID, reminder.Name, req.GetActorType())
		return o.deleteXNSReminder(ctx, reminder.Name)
	}
	targetAppID := req.GetTargetAppId()

	start := time.Now()
	err := o.xnsDispatcher.DispatchWorkflow(ctx, targetNs, targetAppID, &req)
	elapsed := diag.ElapsedSince(start)
	if err == nil {
		diag.DefaultWorkflowMonitoring.WorkflowOperationEvent(ctx, diag.XNSDispatch, diag.StatusSuccess, elapsed)
		return o.deleteXNSReminder(ctx, reminder.Name)
	}

	code := status.Code(err)
	switch code {
	case codes.AlreadyExists:
		// Target already has a reminder for this key — the hop is durable.
		diag.DefaultWorkflowMonitoring.WorkflowOperationEvent(ctx, diag.XNSDispatch, diag.StatusSuccess, elapsed)
		return o.deleteXNSReminder(ctx, reminder.Name)
	case codes.PermissionDenied:
		log.Warnf("Workflow actor '%s': cross-ns dispatch to '%s/%s' denied by policy: %v", o.actorID, targetNs, targetAppID, err)
		diag.DefaultWorkflowMonitoring.WorkflowOperationEvent(ctx, diag.XNSDispatch, diag.StatusFailed, elapsed)
		if ferr := o.failXNSDispatch(ctx, &req, "WorkflowAccessPolicyDenied"); ferr != nil {
			return fmt.Errorf("failed to signal xns policy denial: %w", ferr)
		}
		return o.deleteXNSReminder(ctx, reminder.Name)
	case codes.Unimplemented:
		log.Errorf("Workflow actor '%s': cross-ns dispatch to '%s/%s' rejected: target sidecar does not support cross-namespace workflow invocation", o.actorID, targetNs, targetAppID)
		diag.DefaultWorkflowMonitoring.WorkflowOperationEvent(ctx, diag.XNSDispatch, diag.StatusFailed, elapsed)
		if ferr := o.failXNSDispatch(ctx, &req, "CrossNamespaceUnsupported"); ferr != nil {
			return fmt.Errorf("failed to signal xns unsupported: %w", ferr)
		}
		return o.deleteXNSReminder(ctx, reminder.Name)
	default:
		// Transient. Leave the reminder in place; failure policy retries
		// up to xnsMaxRetries before the scheduler stops firing it.
		diag.DefaultWorkflowMonitoring.WorkflowOperationEvent(ctx, diag.XNSDispatch, diag.StatusRecoverable, elapsed)
		log.Warnf("Workflow actor '%s': cross-ns dispatch transient failure to '%s/%s': %v (will retry)", o.actorID, targetNs, targetAppID, err)
		return err
	}
}

// handleXNSResultReminder fires on the target side when a result hop is due.
// It ships the buffered result event back to the parent's sidecar via SI.
func (o *orchestrator) handleXNSResultReminder(ctx context.Context, reminder *actorapi.Reminder) error {
	if o.xnsDispatcher == nil {
		return errors.New("cross-namespace dispatcher not configured")
	}

	var req internalsv1pb.CrossNSResultRequest
	if reminder.Data == nil {
		return o.deleteXNSReminder(ctx, reminder.Name)
	}
	if err := proto.Unmarshal(reminder.Data.GetValue(), &req); err != nil {
		log.Errorf("Workflow actor '%s': cross-ns result reminder '%s' has malformed payload: %v", o.actorID, reminder.Name, err)
		return o.deleteXNSReminder(ctx, reminder.Name)
	}

	parentNs := req.GetSource().GetNamespace()
	parentAppID := req.GetTargetAppId()

	start := time.Now()
	err := o.xnsDispatcher.DeliverResult(ctx, parentNs, parentAppID, &req)
	elapsed := diag.ElapsedSince(start)
	if err == nil {
		diag.DefaultWorkflowMonitoring.WorkflowOperationEvent(ctx, diag.XNSResult, diag.StatusSuccess, elapsed)
		return o.deleteXNSReminder(ctx, reminder.Name)
	}

	code := status.Code(err)
	switch code {
	case codes.AlreadyExists, codes.OK:
		diag.DefaultWorkflowMonitoring.WorkflowOperationEvent(ctx, diag.XNSResult, diag.StatusSuccess, elapsed)
		return o.deleteXNSReminder(ctx, reminder.Name)
	case codes.PermissionDenied, codes.Unimplemented, codes.NotFound:
		// Terminal: parent refuses / is gone. Drop rather than retry
		// indefinitely.
		diag.DefaultWorkflowMonitoring.WorkflowOperationEvent(ctx, diag.XNSResult, diag.StatusFailed, elapsed)
		log.Warnf("Workflow actor '%s': cross-ns result to '%s/%s' dropped: %v", o.actorID, parentNs, parentAppID, err)
		return o.deleteXNSReminder(ctx, reminder.Name)
	default:
		diag.DefaultWorkflowMonitoring.WorkflowOperationEvent(ctx, diag.XNSResult, diag.StatusRecoverable, elapsed)
		log.Warnf("Workflow actor '%s': cross-ns result transient failure to '%s/%s': %v (will retry)", o.actorID, parentNs, parentAppID, err)
		return err
	}
}

// handleXNSResultInReminder fires on the parent side when
// DeliverWorkflowResultCrossNamespace has scheduled a result delivery.
// Validates the carried parent executionId against the parent
// orchestrator's currently-loaded state, drops the event if the parent was
// terminated+purged+re-created since the dispatch, and otherwise delivers
// the event into the inbox via the standard addWorkflowEvent path.
func (o *orchestrator) handleXNSResultInReminder(ctx context.Context, reminder *actorapi.Reminder) error {
	var req internalsv1pb.CrossNSResultRequest
	if reminder.Data == nil {
		return o.deleteXNSReminder(ctx, reminder.Name)
	}
	if err := proto.Unmarshal(reminder.Data.GetValue(), &req); err != nil {
		log.Errorf("Workflow actor '%s': cross-ns result-in reminder '%s' malformed: %v", o.actorID, reminder.Name, err)
		return o.deleteXNSReminder(ctx, reminder.Name)
	}

	// ExecutionId isolation:
	//   - Parent state missing entirely → parent was purged before the
	//     result returned; drop the result rather than calling
	//     addWorkflowEvent on a non-existent state (which would create a
	//     phantom inbox entry on a workflow nobody is waiting for).
	//   - Parent state present but executionId mismatch → parent was
	//     terminate+purge+re-created with a fresh run. Drop, since the
	//     new run never dispatched this child.
	state, _, lerr := o.loadInternalState(ctx)
	if lerr == nil && state == nil {
		log.Warnf("Workflow actor '%s': dropping cross-ns result: parent instance no longer exists", o.actorID)
		return o.deleteXNSReminder(ctx, reminder.Name)
	}
	currentExecID := ""
	if rs := o.rstate; rs != nil {
		currentExecID = rs.GetStartEvent().GetWorkflowInstance().GetExecutionId().GetValue()
	}
	if currentExecID != "" && req.GetParentExecutionId() != "" && currentExecID != req.GetParentExecutionId() {
		log.Warnf("Workflow actor '%s': dropping stale cross-ns result (executionId '%s' != current '%s')", o.actorID, req.GetParentExecutionId(), currentExecID)
		return o.deleteXNSReminder(ctx, reminder.Name)
	}

	var ev backend.HistoryEvent
	if err := proto.Unmarshal(req.GetEvent(), &ev); err != nil {
		log.Errorf("Workflow actor '%s': cross-ns result-in event unmarshal failed: %v", o.actorID, err)
		return o.deleteXNSReminder(ctx, reminder.Name)
	}
	if err := o.addWorkflowEvent(ctx, &ev); err != nil {
		// Leave the reminder in place so the retry policy re-delivers.
		return fmt.Errorf("failed to deliver cross-ns result into inbox: %w", err)
	}
	return o.deleteXNSReminder(ctx, reminder.Name)
}

// handleXNSExecReminder fires on the target side to carry a cross-ns dispatch
// into the local orchestrator/activity actor via a normal local invocation.
// The reminder name is the idempotency key; duplicate creates are no-ops, so
// firing is exactly-once even under caller retries.
func (o *orchestrator) handleXNSExecReminder(ctx context.Context, reminder *actorapi.Reminder) error {
	var req internalsv1pb.CrossNSDispatchRequest
	if reminder.Data == nil {
		return o.deleteXNSReminder(ctx, reminder.Name)
	}
	if err := proto.Unmarshal(reminder.Data.GetValue(), &req); err != nil {
		log.Errorf("Workflow actor '%s': cross-ns exec reminder '%s' has malformed payload: %v", o.actorID, reminder.Name, err)
		return o.deleteXNSReminder(ctx, reminder.Name)
	}

	// Re-evaluate the WorkflowAccessPolicy at fire time. The ingress
	// handler (CallWorkflowCrossNamespace) checked it before scheduling
	// this reminder, but a policy reload between ingress and reminder
	// fire could revoke the caller's permission — and an xns-dispatch
	// reminder may sit in retry for up to xnsRetryInterval * xnsMaxRetries
	// before firing here. The check uses the SPIFFE-stamped caller
	// identity carried on the reminder's Source, not arbitrary metadata.
	if denied, err := o.xnsExecPolicyDenied(&req); err != nil {
		log.Errorf("Workflow actor '%s': cross-ns exec policy check failed: %v", o.actorID, err)
		return o.deleteXNSReminder(ctx, reminder.Name)
	} else if denied {
		log.Warnf("Workflow actor '%s': cross-ns exec denied by policy at fire time (caller %s/%s)", o.actorID, req.GetSource().GetNamespace(), req.GetSource().GetAppId())
		return o.deleteXNSReminder(ctx, reminder.Name)
	}

	invocation := internalsv1pb.
		NewInternalInvokeRequest(req.GetMethod()).
		WithActor(req.GetActorType(), req.GetActorId()).
		WithData(req.GetPayload()).
		WithContentType(invokev1.ProtobufContentType)

	// Forward only the keys the bridge explicitly supports. A peer that
	// stuffs arbitrary metadata into the dispatch request must not be able
	// to influence local actor invocation behaviour beyond the well-known
	// flags this bridge already documents (e.g. RecursivePurge's force).
	if filtered := filterXNSInvocationMetadata(req.GetMethod(), req.GetInvocationMetadata()); len(filtered) > 0 {
		invocation.Metadata = filtered
	}

	// Stamp the cross-ns caller's identity on the local invocation so the
	// per-method access policy check inside handleInvoke evaluates against
	// the actual caller (carried on req.Source) rather than seeing an empty
	// identity. The Source itself was authenticated via SPIFFE on the
	// CallWorkflowCrossNamespace ingress. Set after metadata so identity
	// stamping is the last writer and cannot be overridden.
	if src := req.GetSource(); src != nil {
		workflowacl.SetCallerIdentity(invocation, src.GetAppId(), src.GetNamespace())
	}

	// The xns-exec reminder fires on the same actor we need to deliver the
	// dispatched method to (the target child workflow/activity). Routing
	// through the local router would re-enter this actor's lock, which we
	// already hold via InvokeReminder, and deadlock. Invoke the handler
	// directly instead. Security is enforced at two points: ingress
	// (CallWorkflowCrossNamespace) and again above at fire time, both
	// against the SPIFFE-stamped caller identity carried on req.Source.
	if _, err := o.handleInvoke(ctx, invocation); err != nil {
		log.Warnf("Workflow actor '%s': cross-ns exec local call failed: %v", o.actorID, err)
		return err
	}
	return o.deleteXNSReminder(ctx, reminder.Name)
}

// xnsExecPolicyDenied re-evaluates the WorkflowAccessPolicy at xns-exec
// fire time. Returns (denied, error). When the policy holder is nil, the
// answer is "denied" (cross-namespace requires an explicit policy on this
// sidecar). When the caller identity is missing from the request, the
// answer is also "denied" — the ingress already verified SPIFFE identity,
// so an absent Source would only happen on a malformed reminder.
func (o *orchestrator) xnsExecPolicyDenied(req *internalsv1pb.CrossNSDispatchRequest) (bool, error) {
	if o.workflowAccessPolicies == nil {
		return true, nil
	}
	policies := o.workflowAccessPolicies.Load()
	if policies == nil {
		return true, nil
	}
	caller := req.GetSource()
	if caller == nil || caller.GetAppId() == "" {
		return true, nil
	}

	opType, _, _, ok := workflowacl.SplitActorType(req.GetActorType())
	if !ok {
		return true, nil
	}
	op, err := xnsExecOperation(req.GetMethod(), req.GetPayload())
	if err != nil {
		return true, err
	}
	opName, err := xnsExecOpName(opType, req.GetMethod(), req.GetPayload())
	if err != nil {
		return true, err
	}
	allowed := policies.Evaluate(caller.GetAppId(), caller.GetNamespace(), opType, op, opName)
	return !allowed, nil
}

// xnsInvocationMetadataAllowList enumerates, per cross-namespace method,
// the metadata keys the bridge will forward onto the local actor
// invocation. Anything not listed here is dropped on the receiving side.
// Adding a new key is a deliberate authorization decision: the receiving
// app accepts the flag's effect on every dispatch from a permitted peer.
var xnsInvocationMetadataAllowList = map[string]map[string]struct{}{
	todo.RecursivePurgeWorkflowStateMethod: {
		todo.MetadataPurgeForce: {},
	},
}

// filterXNSInvocationMetadata returns a copy of md restricted to the keys
// the bridge has authorized for method. Returns nil when there is nothing
// to forward, so callers can leave the invocation's Metadata field unset.
func filterXNSInvocationMetadata(method string, md map[string]*internalsv1pb.ListStringValue) map[string]*internalsv1pb.ListStringValue {
	allowed, ok := xnsInvocationMetadataAllowList[method]
	if !ok || len(md) == 0 {
		return nil
	}
	out := make(map[string]*internalsv1pb.ListStringValue, len(allowed))
	for k, v := range md {
		if _, permitted := allowed[k]; !permitted {
			continue
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// xnsExecOperation maps the inner method dispatched cross-namespace onto the
// WorkflowOperation used by the policy. Mirrors the ingress-side logic in
// pkg/api/grpc/workflow_xns.go so the two checkpoints behave identically.
// AddWorkflowEvent's operation depends on the carried HistoryEvent type, so
// the payload is required to resolve it.
func xnsExecOperation(method string, payload []byte) (wfaclapi.WorkflowOperation, error) {
	switch method {
	case todo.CreateWorkflowInstanceMethod, todo.ExecuteActivityMethod:
		return wfaclapi.WorkflowOperationSchedule, nil
	case todo.RecursivePurgeWorkflowStateMethod:
		return wfaclapi.WorkflowOperationPurge, nil
	case todo.AddWorkflowEventMethod:
		var ev backend.HistoryEvent
		if err := proto.Unmarshal(payload, &ev); err != nil {
			return "", fmt.Errorf("failed to unmarshal AddWorkflowEvent payload: %w", err)
		}
		return workflowacl.OperationFromHistoryEvent(&ev)
	default:
		return "", fmt.Errorf("unsupported cross-ns method %q", method)
	}
}

// xnsExecOpName extracts the workflow or activity name from the dispatched
// payload so the policy can match per-name rules at exec time. Recursive
// purge and AddWorkflowEvent only carry an instanceID at this layer (the
// workflow name lives in the local state, which we deliberately do not load
// at policy-check time); they fall back to "*" so only wildcard rules apply.
func xnsExecOpName(opType workflowacl.OperationType, method string, payload []byte) (string, error) {
	switch method {
	case todo.CreateWorkflowInstanceMethod:
		return workflowacl.WorkflowNameFromCreateRequest(payload)
	case todo.ExecuteActivityMethod:
		return workflowacl.ActivityNameFromExecute(method, payload)
	case todo.RecursivePurgeWorkflowStateMethod, todo.AddWorkflowEventMethod:
		return "*", nil
	default:
		return "", fmt.Errorf("unsupported cross-ns method %q", method)
	}
}

// deleteXNSReminder removes a completed xns reminder from the scheduler so
// it doesn't re-fire.
func (o *orchestrator) deleteXNSReminder(ctx context.Context, name string) error {
	return o.reminders.Delete(ctx, &actorapi.DeleteReminderRequest{
		ActorType: o.actorType,
		ActorID:   o.actorID,
		Name:      name,
	})
}

// shipCrossNSResult is called on the target (child) side when a child
// workflow completion/failure event needs to be delivered to a parent
// orchestrator in a different namespace. It creates a durable xns-result
// reminder on the local (child) orchestrator actor. When the reminder
// fires, handleXNSResultReminder performs the service-invocation hop to
// the parent sidecar's DeliverWorkflowResultCrossNamespace endpoint.
func (o *orchestrator) shipCrossNSResult(ctx context.Context, event *backend.HistoryEvent, parentAppID, parentNamespace, parentInstanceID string, marshaledEvent []byte) error {
	var parentExecID string
	if rs := o.rstate; rs != nil {
		if es := rs.GetStartEvent(); es != nil {
			if pi := es.GetParentInstance(); pi != nil {
				parentExecID = pi.GetWorkflowInstance().GetExecutionId().GetValue()
			}
		}
	}

	childExecID := ""
	if rs := o.rstate; rs != nil {
		childExecID = rs.GetStartEvent().GetWorkflowInstance().GetExecutionId().GetValue()
	}

	var taskScheduledID int32
	switch {
	case event.GetChildWorkflowInstanceCompleted() != nil:
		taskScheduledID = event.GetChildWorkflowInstanceCompleted().GetTaskScheduledId()
	case event.GetChildWorkflowInstanceFailed() != nil:
		taskScheduledID = event.GetChildWorkflowInstanceFailed().GetTaskScheduledId()
	default:
		taskScheduledID = event.GetEventId()
	}

	key := DeterministicXNSKey(
		o.actorTypeBuilder.Namespace(), o.appID,
		parentInstanceID, parentExecID,
		o.actorID, childExecID,
		taskScheduledID, XNSHopResult,
	)

	req := &internalsv1pb.CrossNSResultRequest{
		IdempotencyKey:    key,
		TargetAppId:       parentAppID,
		Event:             marshaledEvent,
		ParentInstanceId:  parentInstanceID,
		ParentExecutionId: parentExecID,
		Source: &internalsv1pb.CrossNSSource{
			Namespace:      parentNamespace,
			AppId:          parentAppID,
			OrchestratorId: o.actorID,
			ExecutionId:    childExecID,
			TaskId:         taskScheduledID,
		},
	}

	name := common.ReminderPrefixXNSResult + key
	if err := o.createXNSReminder(ctx, name, req); err != nil {
		return fmt.Errorf("failed to create cross-ns result reminder: %w", err)
	}
	log.Debugf("Workflow actor '%s': scheduled cross-ns result to '%s/%s' key=%s", o.actorID, parentNamespace, parentAppID, key)
	return nil
}

// dispatchCrossNSCreate is called when the outbound message is a
// CreateWorkflowInstanceRequest bound for a child orchestrator in another
// namespace.
func (o *orchestrator) dispatchCrossNSCreate(ctx context.Context, router *protos.TaskRouter, req *backend.CreateWorkflowInstanceRequest, childInstanceID string, marshaledReq []byte) error {
	targetNs := router.GetTargetAppNamespace()
	targetAppID := router.GetTargetAppID()
	targetActorType := o.actorTypeBuilder.WorkflowNS(targetNs, targetAppID)

	startEvent := req.GetStartEvent()
	childExec := startEvent.GetExecutionStarted().GetWorkflowInstance().GetExecutionId().GetValue()

	parentExec := ""
	if rs := o.rstate; rs != nil {
		parentExec = rs.GetStartEvent().GetWorkflowInstance().GetExecutionId().GetValue()
	}

	var taskID int32
	if es := startEvent.GetExecutionStarted(); es != nil && es.GetParentInstance() != nil {
		taskID = es.GetParentInstance().GetTaskScheduledId()
	} else {
		taskID = startEvent.GetEventId()
	}

	return o.dispatchCrossNS(ctx,
		targetNs, targetAppID,
		targetActorType, childInstanceID,
		todo.CreateWorkflowInstanceMethod,
		marshaledReq,
		nil,
		parentExec, childInstanceID, childExec,
		taskID,
	)
}

// failXNSDispatch surfaces a terminal cross-namespace dispatch rejection
// (policy-denied or feature-unsupported) back to the parent orchestrator's
// inbox by creating a synthetic TaskFailed/ChildWorkflowInstanceFailed
// reminder. The reminder fires in a fresh execution cycle, reusing the
// same activity-result path the engine already uses for same-namespace
// failures.
func (o *orchestrator) failXNSDispatch(ctx context.Context, req *internalsv1pb.CrossNSDispatchRequest, errorType string) error {
	taskScheduledID := req.GetSource().GetTaskId()
	errMsg := "cross-namespace workflow invocation failed: " + errorType

	actorType := req.GetActorType()
	var failedEvent *protos.HistoryEvent
	switch {
	case strings.HasSuffix(actorType, ".workflow"):
		failedEvent = &protos.HistoryEvent{
			EventId:   -1,
			Timestamp: timestamppb.New(time.Now()),
			EventType: &protos.HistoryEvent_ChildWorkflowInstanceFailed{
				ChildWorkflowInstanceFailed: &protos.ChildWorkflowInstanceFailedEvent{
					TaskScheduledId: taskScheduledID,
					FailureDetails: &protos.TaskFailureDetails{
						ErrorType:    errorType,
						ErrorMessage: errMsg,
					},
				},
			},
		}
	case strings.HasSuffix(actorType, ".activity"):
		failedEvent = &protos.HistoryEvent{
			EventId:   -1,
			Timestamp: timestamppb.New(time.Now()),
			EventType: &protos.HistoryEvent_TaskFailed{
				TaskFailed: &protos.TaskFailedEvent{
					TaskScheduledId: taskScheduledID,
					FailureDetails: &protos.TaskFailureDetails{
						ErrorType:    errorType,
						ErrorMessage: errMsg,
					},
				},
			},
		}
	default:
		log.Warnf("Workflow actor '%s': failXNSDispatch: unexpected target actor type '%s', skipping failure synthesis", o.actorID, actorType)
		return nil
	}

	if _, err := o.createWorkflowReminder(ctx, common.ReminderPrefixActivityResult, failedEvent, time.Now(), o.appID, nil); err != nil {
		return fmt.Errorf("failed to create xns failure reminder: %w", err)
	}
	return nil
}
