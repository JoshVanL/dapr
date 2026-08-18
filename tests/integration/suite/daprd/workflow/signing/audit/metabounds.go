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

package audit

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	wferrors "github.com/dapr/dapr/pkg/runtime/wfengine/state/errors"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	fworkflow "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
	dworkflow "github.com/dapr/durabletask-go/workflow"
)

func init() {
	suite.Register(new(metabounds))
}

// metabounds verifies the audit detects a metadata row inflated at the db
// level (an OOM-attack shape: history length far beyond the allowed bound)
// on a resident actor, without a restart or client event. The inflated
// metadata makes the whole persisted state unreadable, so the tombstone is
// written over a fresh state whose only event is the tamper marker; with no
// readable ExecutionStarted event the workflow surfaces as Pending rather
// than Failed (the same presentation as cold-load bounds detection), so the
// persisted marker and the audit metric are asserted directly.
type metabounds struct {
	workflow *workflow.Workflow
}

func (m *metabounds) Setup(t *testing.T) []framework.Option {
	m.workflow = workflow.New(t,
		workflow.WithMTLS(t),
		workflow.WithSigningAuditInterval("1s"),
	)

	return []framework.Option{
		framework.WithProcesses(m.workflow),
	}
}

func (m *metabounds) Run(t *testing.T, ctx context.Context) {
	m.workflow.WaitUntilRunning(t, ctx)

	client := m.workflow.WorkflowClient(t, ctx)
	id := fworkflow.StartParkedWorkflow(t, ctx, client, fworkflow.ParkedRegistry("audit-metabounds"), "audit-metabounds")

	fworkflow.MutateMetadata(t, ctx, m.workflow.DB(), id, func(md *backend.BackendWorkflowStateMetadata) {
		md.HistoryLength = 2_000_000
	})

	// The audit alone must detect the bounds violation and tombstone the
	// workflow.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, fworkflow.IntegrityAuditCount(c, ctx, m.workflow.Dapr(), "tampered"), 1.0)
	}, time.Second*20, time.Millisecond*100)

	// The tombstone rewrote the metadata to cover only the marker event.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		var meta backend.BackendWorkflowStateMetadata
		_, raw := m.workflow.DB().ReadStateValue(t, ctx, id, "metadata")
		if !assert.NoError(c, proto.Unmarshal(raw, &meta)) {
			return
		}
		assert.Equal(c, uint64(1), meta.GetHistoryLength())
	}, time.Second*10, time.Millisecond*100)

	_, raw := m.workflow.DB().FirstStateValue(t, ctx, id, "history")
	var evt protos.HistoryEvent
	require.NoError(t, proto.Unmarshal(raw, &evt))
	ec := evt.GetExecutionCompleted()
	require.NotNil(t, ec, "persisted history must hold the tamper marker")
	assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_FAILED, ec.GetWorkflowStatus())
	assert.Equal(t, wferrors.ErrorTypeHistoryTampered, ec.GetFailureDetails().GetErrorType())

	// State is loadable again (the marker bypasses verification) and, with no
	// readable start event, surfaces as Pending. No further progress happens.
	meta, err := client.FetchWorkflowMetadata(ctx, id)
	require.NoError(t, err)
	require.NotNil(t, meta)
	assert.Equal(t, dworkflow.StatusPending, meta.RuntimeStatus)
}
