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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	workflowacl "github.com/dapr/dapr/pkg/acl/workflow"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
	wfaclapi "github.com/dapr/dapr/pkg/apis/workflowaccesspolicy/v1alpha1"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
)

// actorTypeBuilderForTest is a tiny constructor used only by these tests
// to wire a minimal orchestrator with the right namespace plumbing.
func actorTypeBuilderForTest(ns string) *common.ActorTypeBuilder {
	return common.NewActorTypeBuilder(ns)
}

// TestXNSExecOperation_OnlyKnownMethods rejects anything other than the
// methods the cross-namespace bridge currently supports. The intent is that
// adding new op kinds is a deliberate proto+policy change, not an accident.
// AddWorkflowEvent's operation is encoded in the carried HistoryEvent type,
// so it has its own coverage below.
func TestXNSExecOperation_OnlyKnownMethods(t *testing.T) {
	cases := []struct {
		method string
		want   wfaclapi.WorkflowOperation
		ok     bool
	}{
		{todo.CreateWorkflowInstanceMethod, wfaclapi.WorkflowOperationSchedule, true},
		{todo.ExecuteActivityMethod, wfaclapi.WorkflowOperationSchedule, true},
		{todo.RecursivePurgeWorkflowStateMethod, wfaclapi.WorkflowOperationPurge, true},
		{todo.PurgeWorkflowStateMethod, "", false},
		{"", "", false},
	}

	for _, tc := range cases {
		t.Run(tc.method, func(t *testing.T) {
			got, err := xnsExecOperation(tc.method, nil)
			if tc.ok {
				require.NoError(t, err)
				assert.Equal(t, tc.want, got)
				return
			}
			assert.Error(t, err)
		})
	}
}

// TestXNSExecOperation_AddWorkflowEventDerivesFromPayload covers the
// AddWorkflowEvent code path: the policy operation comes from the inner
// HistoryEvent type (terminate / raise / suspend / resume) rather than from
// the method name.
func TestXNSExecOperation_AddWorkflowEventDerivesFromPayload(t *testing.T) {
	terminateEvent := &protos.HistoryEvent{
		EventType: &protos.HistoryEvent_ExecutionTerminated{
			ExecutionTerminated: &protos.ExecutionTerminatedEvent{},
		},
	}
	payload, err := proto.Marshal(terminateEvent)
	require.NoError(t, err)

	got, err := xnsExecOperation(todo.AddWorkflowEventMethod, payload)
	require.NoError(t, err)
	assert.Equal(t, wfaclapi.WorkflowOperationTerminate, got)

	_, err = xnsExecOperation(todo.AddWorkflowEventMethod, []byte("not-a-proto"))
	assert.Error(t, err)
}

// TestXNSExecOpName_ExtractsWorkflowName shows that for a
// CreateWorkflowInstance dispatch, opName comes from the marshalled
// CreateWorkflowInstanceRequest start event. Used by the policy
// pattern-match.
func TestXNSExecOpName_ExtractsWorkflowName(t *testing.T) {
	req := &backend.CreateWorkflowInstanceRequest{
		StartEvent: &protos.HistoryEvent{
			EventType: &protos.HistoryEvent_ExecutionStarted{
				ExecutionStarted: &protos.ExecutionStartedEvent{
					Name: "MyWorkflow",
				},
			},
		},
	}
	payload, err := proto.Marshal(req)
	require.NoError(t, err)

	got, err := xnsExecOpName(workflowacl.OperationTypeWorkflow, todo.CreateWorkflowInstanceMethod, payload)
	require.NoError(t, err)
	assert.Equal(t, "MyWorkflow", got)
}

// TestXNSExecOpName_ExtractsActivityName same idea for activities. The
// Execute method's payload is the TaskScheduled event (or wrapped in an
// ActivityInvocation envelope); ActivityNameFromExecute handles both.
func TestXNSExecOpName_ExtractsActivityName(t *testing.T) {
	taskScheduled := &protos.HistoryEvent{
		EventType: &protos.HistoryEvent_TaskScheduled{
			TaskScheduled: &protos.TaskScheduledEvent{Name: "MyActivity"},
		},
	}
	payload, err := proto.Marshal(taskScheduled)
	require.NoError(t, err)

	got, err := xnsExecOpName(workflowacl.OperationTypeActivity, todo.ExecuteActivityMethod, payload)
	require.NoError(t, err)
	assert.Equal(t, "MyActivity", got)
}

// TestXNSExecOpName_RejectsUnsupportedMethod confirms the negative path.
// xnsExecOpName must never silently default to "" for an unknown method
// because policy.Evaluate("") would silently grant access via "" pattern
// matches.
func TestXNSExecOpName_RejectsUnsupportedMethod(t *testing.T) {
	_, err := xnsExecOpName(workflowacl.OperationTypeWorkflow, "Unknown", nil)
	assert.Error(t, err)
}

// TestFilterXNSInvocationMetadata exercises the receiving-side allow-list
// applied to peer-supplied invocation metadata. Methods not in the
// allow-list drop everything; permitted keys flow through; unrelated keys
// are silently stripped.
func TestFilterXNSInvocationMetadata(t *testing.T) {
	allowed := map[string]*internalsv1pb.ListStringValue{
		todo.MetadataPurgeForce: {Values: []string{"true"}},
	}
	mixed := map[string]*internalsv1pb.ListStringValue{
		todo.MetadataPurgeForce: {Values: []string{"true"}},
		"x-attacker-injected":   {Values: []string{"yes"}},
		invokev1.CallerIDHeader: {Values: []string{"spoofed-app"}},
	}

	t.Run("method outside allow-list drops everything", func(t *testing.T) {
		got := filterXNSInvocationMetadata(todo.CreateWorkflowInstanceMethod, allowed)
		assert.Nil(t, got)
	})

	t.Run("permitted key flows through, unrelated keys stripped", func(t *testing.T) {
		got := filterXNSInvocationMetadata(todo.RecursivePurgeWorkflowStateMethod, mixed)
		assert.Len(t, got, 1)
		assert.Equal(t, []string{"true"}, got[todo.MetadataPurgeForce].GetValues())
		_, present := got["x-attacker-injected"]
		assert.False(t, present)
	})

	t.Run("nil input is nil output", func(t *testing.T) {
		got := filterXNSInvocationMetadata(todo.RecursivePurgeWorkflowStateMethod, nil)
		assert.Nil(t, got)
	})
}

// TestRoutingKind_Classification covers the three branches that the
// orchestrator's scheduling code pivots on.
func TestRoutingKind_Classification(t *testing.T) {
	o := &orchestrator{
		actorID: "wf-1",
	}
	o.factory = &factory{
		appID:            "appA",
		actorTypeBuilder: actorTypeBuilderForTest("nsA"),
	}

	t.Run("nil router → Local", func(t *testing.T) {
		assert.Equal(t, RoutingLocal, o.classifyRouting(nil))
	})

	t.Run("router for same app same ns → Local", func(t *testing.T) {
		r := &protos.TaskRouter{SourceAppID: "appA"}
		assert.Equal(t, RoutingLocal, o.classifyRouting(r))
	})

	t.Run("router targeting same-ns different app → CrossApp", func(t *testing.T) {
		other := "appB"
		r := &protos.TaskRouter{SourceAppID: "appA", TargetAppID: &other}
		assert.Equal(t, RoutingCrossApp, o.classifyRouting(r))
	})

	t.Run("router targeting foreign ns → CrossNS", func(t *testing.T) {
		other := "appB"
		ns := "nsB"
		r := &protos.TaskRouter{
			SourceAppID:        "appA",
			TargetAppID:        &other,
			TargetAppNamespace: &ns,
		}
		assert.Equal(t, RoutingCrossNS, o.classifyRouting(r))
	})

	t.Run("foreign ns equal to local ns → CrossApp not CrossNS", func(t *testing.T) {
		other := "appB"
		ns := "nsA"
		r := &protos.TaskRouter{
			SourceAppID:        "appA",
			TargetAppID:        &other,
			TargetAppNamespace: &ns,
		}
		assert.Equal(t, RoutingCrossApp, o.classifyRouting(r))
	})
}
