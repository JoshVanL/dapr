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

package workflow

import (
	"errors"
	"fmt"
	"strings"

	"google.golang.org/protobuf/proto"

	wfaclapi "github.com/dapr/dapr/pkg/apis/workflowaccesspolicy/v1alpha1"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/durabletask-go/backend"
)

const (
	actorTypePrefix = "dapr.internal."
	suffixWorkflow  = ".workflow"
	suffixActivity  = ".activity"
)

// OperationType represents the type of workflow operation being performed.
type OperationType string

const (
	OperationTypeWorkflow OperationType = "workflow"
	OperationTypeActivity OperationType = "activity"
)

// ParseActorType determines if an actor type represents a workflow or activity
// actor. Returns the operation type and true if it is a workflow/activity actor,
// or empty string and false otherwise.
func ParseActorType(actorType string) (OperationType, bool) {
	op, _, _, ok := SplitActorType(actorType)
	return op, ok
}

// SplitActorType extracts the (operation, namespace, appID) triple from a
// workflow/activity actor type string of the form
// "dapr.internal.<namespace>.<appID>.<workflow|activity>". Returns ok=false
// if the input doesn't match that shape. AppIDs may not contain dots; the
// renderer always emits "<ns>.<app>.<kind>" so the last dot-separated
// segment before the suffix is the appID and everything before it is the
// namespace (which may itself contain dots).
func SplitActorType(actorType string) (op OperationType, namespace, appID string, ok bool) {
	if !strings.HasPrefix(actorType, actorTypePrefix) {
		return "", "", "", false
	}
	rest := actorType[len(actorTypePrefix):]
	switch {
	case strings.HasSuffix(rest, suffixWorkflow):
		op = OperationTypeWorkflow
		rest = strings.TrimSuffix(rest, suffixWorkflow)
	case strings.HasSuffix(rest, suffixActivity):
		op = OperationTypeActivity
		rest = strings.TrimSuffix(rest, suffixActivity)
	default:
		return "", "", "", false
	}
	dot := strings.LastIndexByte(rest, '.')
	if dot <= 0 || dot == len(rest)-1 {
		return op, "", "", true
	}
	return op, rest[:dot], rest[dot+1:], true
}

// WorkflowOperationFromMethod returns the WorkflowOperation for a workflow
// actor method. An empty operation with nil error means the method is not
// subject to access control (an internal/system method). AddWorkflowEvent's
// operation is encoded in the HistoryEvent payload; parsedAddEvent must be
// non-nil for that method so we don't unmarshal twice on the hot path.
func WorkflowOperationFromMethod(method string, parsedAddEvent *backend.HistoryEvent) (wfaclapi.WorkflowOperation, error) {
	switch method {
	case todo.CreateWorkflowInstanceMethod:
		return wfaclapi.WorkflowOperationSchedule, nil

	case todo.AddWorkflowEventMethod:
		if parsedAddEvent == nil {
			return "", errors.New("AddWorkflowEvent: parsed event is required to derive the operation")
		}
		return OperationFromHistoryEvent(parsedAddEvent)

	case todo.PurgeWorkflowStateMethod:
		return wfaclapi.WorkflowOperationPurge, nil

	case todo.WaitForRuntimeStatus, todo.GetWorkflowMetadataMethod:
		return wfaclapi.WorkflowOperationGet, nil

	case todo.ForkWorkflowHistory, todo.RerunWorkflowInstance:
		return wfaclapi.WorkflowOperationRerun, nil

	default:
		return "", nil
	}
}

// ActivityNameFromExecute returns the activity name from an Execute method
// payload. An empty name with nil error means the method is not Execute
// (no other activity methods are subject to access control).
func ActivityNameFromExecute(method string, data []byte) (string, error) {
	if method != todo.ExecuteActivityMethod {
		return "", nil
	}

	var his backend.HistoryEvent
	if err := proto.Unmarshal(data, &his); err != nil {
		return "", fmt.Errorf("failed to unmarshal activity HistoryEvent: %w", err)
	}
	ts := his.GetTaskScheduled()
	if ts == nil {
		return "", errors.New("activity HistoryEvent missing TaskScheduled")
	}
	return ts.GetName(), nil
}

func WorkflowNameFromCreateRequest(data []byte) (string, error) {
	var req backend.CreateWorkflowInstanceRequest
	if err := proto.Unmarshal(data, &req); err != nil {
		return "", fmt.Errorf("failed to unmarshal CreateWorkflowInstanceRequest: %w", err)
	}
	es := req.GetStartEvent().GetExecutionStarted()
	if es == nil {
		return "", errors.New("CreateWorkflowInstanceRequest missing ExecutionStarted event")
	}
	return es.GetName(), nil
}

// OperationFromHistoryEvent maps an AddWorkflowEvent payload's HistoryEvent
// onto the WorkflowOperation a policy rule must allow. Used by the local
// access policy check and by the cross-namespace bridge so both reach the
// same authorization decision for the same payload.
func OperationFromHistoryEvent(ev *backend.HistoryEvent) (wfaclapi.WorkflowOperation, error) {
	switch {
	case ev.GetExecutionTerminated() != nil:
		return wfaclapi.WorkflowOperationTerminate, nil
	case ev.GetEventRaised() != nil:
		return wfaclapi.WorkflowOperationRaise, nil
	case ev.GetExecutionSuspended() != nil:
		return wfaclapi.WorkflowOperationPause, nil
	case ev.GetExecutionResumed() != nil:
		return wfaclapi.WorkflowOperationResume, nil
	default:
		return "", fmt.Errorf("AddWorkflowEvent HistoryEvent has unsupported event type %T", ev.GetEventType())
	}
}
