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
	dworkflow "github.com/dapr/durabletask-go/workflow"
)

func init() {
	suite.Register(new(history))
}

// history verifies that a history event tampered at the db level is detected
// while the orchestrator actor stays resident with its cached state: the
// background integrity audit re-reads the store on its interval, detects the
// broken signature chain and terminally fails the workflow. daprd is never
// restarted and no workflow event is raised before detection, so the
// cold-load verification path can not be the detector.
type history struct {
	workflow *workflow.Workflow
}

func (h *history) Setup(t *testing.T) []framework.Option {
	h.workflow = workflow.New(t,
		workflow.WithMTLS(t),
		workflow.WithSigningAuditInterval("1s"),
	)

	return []framework.Option{
		framework.WithProcesses(h.workflow),
	}
}

func (h *history) Run(t *testing.T, ctx context.Context) {
	h.workflow.WaitUntilRunning(t, ctx)

	client := h.workflow.WorkflowClient(t, ctx)
	id := fworkflow.StartParkedWorkflow(t, ctx, client, fworkflow.ParkedRegistry("audit-history"), "audit-history")

	// Tamper with a history event at the db level while the actor stays
	// resident with its cached (clean) state.
	histKey, raw := h.workflow.DB().FirstStateValue(t, ctx, id, "history")

	var evt protos.HistoryEvent
	require.NoError(t, proto.Unmarshal(raw, &evt))

	evt.EventId += 9999

	updated, err := proto.Marshal(&evt)
	require.NoError(t, err)

	h.workflow.DB().WriteStateValue(t, ctx, histKey, updated)

	// No restart and no event: the background audit alone must detect the
	// tampering within its interval and tombstone the workflow.
	fworkflow.WaitForTampered(t, ctx, client, id, time.Second*20)

	// Raising the awaited event must not resurrect the workflow: the tamper
	// tombstone deleted its reminders and the state is terminal.
	//nolint:errcheck
	client.RaiseEvent(ctx, id, "continue", dworkflow.WithEventPayload("real-event"))

	meta, err := client.FetchWorkflowMetadata(ctx, id)
	require.NoError(t, err)
	require.NotNil(t, meta)
	assert.Equal(t, dworkflow.StatusFailed, meta.RuntimeStatus)
}
