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

package activity

import (
	"context"
	"errors"
	"fmt"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	actorapi "github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
	xnscommon "github.com/dapr/dapr/pkg/actors/targets/workflow/common/xns"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
)

// xnsRetryInterval and xnsMaxRetries match the orchestrator-side bounded
// failure policy so cross-ns reminders share the same retry budget across
// orchestrator and activity actors.
const (
	xnsRetryInterval       = 30 * time.Second
	xnsMaxRetries    uint32 = 60
)

// isCrossNamespaceParent reports whether the activity's source workflow
// lives in a different namespace than this sidecar. The TaskScheduled
// router carries the source identity stamped by the orchestrator-side
// applier; if its namespace is non-empty and does not match ours, the
// completion event must travel back via the bridge instead of via local
// placement.
func (a *activity) isCrossNamespaceParent(taskEvent *backend.HistoryEvent) bool {
	router := taskEvent.GetRouter()
	if router == nil {
		return false
	}
	srcNs := router.GetSourceAppNamespace()
	return srcNs != "" && srcNs != a.actorTypeBuilder.Namespace()
}

// shipCrossNSResult schedules a durable xns-result-* reminder on this
// activity actor. When the reminder fires, handleXNSResultReminder calls
// the Dispatcher to ship the result event to the parent's sidecar.
func (a *activity) shipCrossNSResult(ctx context.Context, taskEvent *backend.HistoryEvent, workflowID string, result *backend.HistoryEvent) error {
	router := taskEvent.GetRouter()
	parentNs := router.GetSourceAppNamespace()
	parentAppID := router.GetSourceAppID()

	if parentAppID == "" || parentNs == "" {
		return fmt.Errorf("activity actor '%s': cross-ns result requires both source app and namespace on the task router", a.actorID)
	}

	resultBytes, err := proto.Marshal(result)
	if err != nil {
		return fmt.Errorf("marshal activity result: %w", err)
	}

	// Activities have no executionId of their own; the parent's
	// executionId (stamped on the TaskRouter by the source workflow's
	// applier when this activity action was emitted) goes in both slots
	// so the deterministic key matches what the parent stamped on
	// dispatch. The parent-side executionId-isolation check uses this
	// to drop stale results targeting a purged-and-recreated parent.
	parentExecID := router.GetSourceExecutionID()

	taskID := taskEvent.GetEventId()
	key := xnscommon.DeterministicKey(
		a.actorTypeBuilder.Namespace(), a.appID,
		workflowID, parentExecID,
		a.actorID, parentExecID,
		taskID, xnscommon.HopResult,
	)

	req := &internalsv1pb.CrossNSResultRequest{
		IdempotencyKey:    key,
		TargetAppId:       parentAppID,
		Event:             resultBytes,
		ParentInstanceId:  workflowID,
		ParentExecutionId: parentExecID,
		Source: &internalsv1pb.CrossNSSource{
			Namespace:      parentNs,
			AppId:          parentAppID,
			OrchestratorId: a.actorID,
			ExecutionId:    parentExecID,
			TaskId:         taskID,
		},
	}
	data, err := anypb.New(req)
	if err != nil {
		return fmt.Errorf("encode xns result reminder: %w", err)
	}

	max := xnsMaxRetries
	name := common.ReminderPrefixXNSResult + key
	return common.CreateReminderWithRetry(ctx, a.reminders, &actorapi.CreateReminderRequest{
		ActorType: a.actorType,
		ActorID:   a.actorID,
		Name:      name,
		Data:      data,
		DueTime:   time.Now().UTC().Format(time.RFC3339),
		FailurePolicy: &commonv1pb.JobFailurePolicy{
			Policy: &commonv1pb.JobFailurePolicy_Constant{
				Constant: &commonv1pb.JobFailurePolicyConstant{
					Interval:   durationpb.New(xnsRetryInterval),
					MaxRetries: &max,
				},
			},
		},
	})
}

// handleXNSResultReminder is the activity-side equivalent of the
// orchestrator's reminder handler: it fires the SI hop to the parent
// sidecar and on success / terminal error deletes the reminder.
func (a *activity) handleXNSResultReminder(ctx context.Context, reminder *actorapi.Reminder) error {
	if a.xnsDispatcher == nil {
		return errors.New("activity: cross-namespace dispatcher not configured")
	}

	if reminder.Data == nil {
		return a.deleteReminder(ctx, reminder.Name)
	}
	var req internalsv1pb.CrossNSResultRequest
	if err := reminder.Data.UnmarshalTo(&req); err != nil {
		log.Errorf("Activity actor '%s': xns result reminder '%s' has malformed payload: %v", a.actorID, reminder.Name, err)
		return a.deleteReminder(ctx, reminder.Name)
	}

	parentNs := req.GetSource().GetNamespace()
	parentAppID := req.GetTargetAppId()

	start := time.Now()
	err := a.xnsDispatcher.DeliverResult(ctx, parentNs, parentAppID, &req)
	elapsed := diag.ElapsedSince(start)
	if err == nil {
		diag.DefaultWorkflowMonitoring.WorkflowOperationEvent(ctx, diag.XNSResult, diag.StatusSuccess, elapsed)
		return a.deleteReminder(ctx, reminder.Name)
	}

	switch status.Code(err) {
	case codes.AlreadyExists, codes.OK:
		diag.DefaultWorkflowMonitoring.WorkflowOperationEvent(ctx, diag.XNSResult, diag.StatusSuccess, elapsed)
		return a.deleteReminder(ctx, reminder.Name)
	case codes.PermissionDenied, codes.Unimplemented, codes.NotFound:
		diag.DefaultWorkflowMonitoring.WorkflowOperationEvent(ctx, diag.XNSResult, diag.StatusFailed, elapsed)
		log.Warnf("Activity actor '%s': cross-ns result to '%s/%s' dropped: %v", a.actorID, parentNs, parentAppID, err)
		return a.deleteReminder(ctx, reminder.Name)
	default:
		diag.DefaultWorkflowMonitoring.WorkflowOperationEvent(ctx, diag.XNSResult, diag.StatusRecoverable, elapsed)
		log.Warnf("Activity actor '%s': cross-ns result transient failure to '%s/%s': %v (will retry)", a.actorID, parentNs, parentAppID, err)
		return err
	}
}

func (a *activity) deleteReminder(ctx context.Context, name string) error {
	return a.reminders.Delete(ctx, &actorapi.DeleteReminderRequest{
		ActorType: a.actorType,
		ActorID:   a.actorID,
		Name:      name,
	})
}

// Compile-time guard that the proto types we rely on are reachable.
var _ = (*protos.HistoryEvent)(nil)
var _ = (*wrapperspb.StringValue)(nil)
