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
	"fmt"
	"strings"

	"google.golang.org/protobuf/proto"

	"github.com/dapr/durabletask-go/backend"
)

const (
	actorTypePrefix = "dapr.internal."
	suffixWorkflow  = ".workflow"
	suffixActivity  = ".activity"

	methodCreateWorkflowInstance = "CreateWorkflowInstance"
	methodExecute                = "Execute"
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
	if !strings.HasPrefix(actorType, actorTypePrefix) {
		return "", false
	}

	switch {
	case strings.HasSuffix(actorType, suffixWorkflow):
		return OperationTypeWorkflow, true
	case strings.HasSuffix(actorType, suffixActivity):
		return OperationTypeActivity, true
	default:
		return "", false
	}
}

// ExtractAppIDFromActorType extracts the app ID from a workflow/activity actor
// type string. The format is "dapr.internal.<namespace>.<appID>.workflow" or
// "dapr.internal.<namespace>.<appID>.activity". The namespace is required to
// correctly split the actor type when either namespace or appID contains dots.
// Returns empty string if the format is not recognized.
func ExtractAppIDFromActorType(actorType string, namespace string) string {
	// Strip the "dapr.internal." prefix.
	rest := strings.TrimPrefix(actorType, actorTypePrefix)
	if rest == actorType {
		return "" // no prefix match
	}

	// Strip the ".workflow" or ".activity" suffix.
	switch {
	case strings.HasSuffix(rest, suffixWorkflow):
		rest = strings.TrimSuffix(rest, suffixWorkflow)
	case strings.HasSuffix(rest, suffixActivity):
		rest = strings.TrimSuffix(rest, suffixActivity)
	default:
		return ""
	}

	// rest is now "<namespace>.<appID>". Strip the known namespace prefix.
	prefix := namespace + "."
	if strings.HasPrefix(rest, prefix) {
		return rest[len(prefix):]
	}

	// Fallback if namespace doesn't match (shouldn't happen in practice).
	if idx := strings.LastIndex(rest, "."); idx >= 0 {
		return rest[idx+1:]
	}
	return rest
}

// ExtractOperationName extracts the workflow or activity name from the request
// method and payload. Returns the name and true if extraction succeeded, or
// empty string and false if the method is not subject to access control
// (e.g. AddWorkflowEvent, PurgeWorkflowState).
func ExtractOperationName(opType OperationType, method string, data []byte) (string, bool, error) {
	switch opType {
	case OperationTypeWorkflow:
		return extractWorkflowName(method, data)
	case OperationTypeActivity:
		return extractActivityName(method, data)
	default:
		return "", false, nil
	}
}

func extractWorkflowName(method string, data []byte) (string, bool, error) {
	if method != methodCreateWorkflowInstance {
		// Only CreateWorkflowInstance is subject to access control (schedule operation).
		return "", false, nil
	}

	var req backend.CreateWorkflowInstanceRequest
	if err := proto.Unmarshal(data, &req); err != nil {
		return "", false, fmt.Errorf("failed to unmarshal CreateWorkflowInstanceRequest: %w", err)
	}

	es := req.GetStartEvent().GetExecutionStarted()
	if es == nil {
		return "", false, fmt.Errorf("CreateWorkflowInstanceRequest missing ExecutionStarted event")
	}

	return es.GetName(), true, nil
}

func extractActivityName(method string, data []byte) (string, bool, error) {
	if method != methodExecute {
		return "", false, nil
	}

	var his backend.HistoryEvent
	if err := proto.Unmarshal(data, &his); err != nil {
		return "", false, fmt.Errorf("failed to unmarshal activity HistoryEvent: %w", err)
	}

	ts := his.GetTaskScheduled()
	if ts == nil {
		return "", false, fmt.Errorf("activity HistoryEvent missing TaskScheduled")
	}

	return ts.GetName(), true, nil
}
