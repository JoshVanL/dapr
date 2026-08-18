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

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	fworkflow "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api/protos"
)

func init() {
	suite.Register(new(disabled))
}

// disabled verifies that auditInterval 0s turns the background audit off:
// history tampered under a resident actor is not tombstoned (direct store
// reads keep surfacing the verification failure instead of a terminal FAILED
// state) and no audit metrics are ever recorded. This pins the opt-out
// semantics and, by contrast with the history test, proves detection there
// really comes from the audit.
type disabled struct {
	workflow *workflow.Workflow
}

func (d *disabled) Setup(t *testing.T) []framework.Option {
	d.workflow = workflow.New(t,
		workflow.WithMTLS(t),
		workflow.WithSigningAuditInterval("0s"),
	)

	return []framework.Option{
		framework.WithProcesses(d.workflow),
	}
}

func (d *disabled) Run(t *testing.T, ctx context.Context) {
	d.workflow.WaitUntilRunning(t, ctx)

	client := d.workflow.WorkflowClient(t, ctx)
	id := fworkflow.StartParkedWorkflow(t, ctx, client, fworkflow.ParkedRegistry("audit-disabled"), "audit-disabled")

	histKey, raw := d.workflow.DB().FirstStateValue(t, ctx, id, "history")

	var evt protos.HistoryEvent
	require.NoError(t, proto.Unmarshal(raw, &evt))

	evt.EventId += 9999

	updated, err := proto.Marshal(&evt)
	require.NoError(t, err)

	d.workflow.DB().WriteStateValue(t, ctx, histKey, updated)

	// Give a would-be auditor several intervals worth of time. With the audit
	// disabled nothing may tombstone the workflow: metadata queries load
	// directly from the store, so they surface the verification failure as an
	// error rather than a terminal FAILED state, for the whole window.
	deadline := time.Now().Add(time.Second * 3)
	for time.Now().Before(deadline) {
		_, err := client.FetchWorkflowMetadata(ctx, id)
		require.Error(t, err, "tampered state must surface as a read error, not a tombstoned FAILED workflow")
		time.Sleep(time.Millisecond * 100)
	}

	assert.Zero(t, fworkflow.IntegrityAuditCount(t, ctx, d.workflow.Dapr(), "verified"))
	assert.Zero(t, fworkflow.IntegrityAuditCount(t, ctx, d.workflow.Dapr(), "tampered"))
}
