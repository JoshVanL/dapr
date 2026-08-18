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

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	fworkflow "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/backend"
)

func init() {
	suite.Register(new(truncate))
}

// truncate verifies the audit detects history truncation at the db level:
// rewriting the metadata row to declare one fewer history event breaks the
// signature chain's full-coverage requirement, so the audit's re-read fails
// verification and tombstones the resident workflow with no restart and no
// client event.
type truncate struct {
	workflow *workflow.Workflow
}

func (tr *truncate) Setup(t *testing.T) []framework.Option {
	tr.workflow = workflow.New(t,
		workflow.WithMTLS(t),
		workflow.WithSigningAuditInterval("1s"),
	)

	return []framework.Option{
		framework.WithProcesses(tr.workflow),
	}
}

func (tr *truncate) Run(t *testing.T, ctx context.Context) {
	tr.workflow.WaitUntilRunning(t, ctx)

	client := tr.workflow.WorkflowClient(t, ctx)
	id := fworkflow.StartParkedWorkflow(t, ctx, client, fworkflow.ActivityParkedRegistry("audit-truncate"), "audit-truncate")

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, fworkflow.SignatureCount(t, ctx, tr.workflow.DB(), id), 2)
	}, time.Second*10, time.Millisecond*100)

	fworkflow.MutateMetadata(t, ctx, tr.workflow.DB(), id, func(m *backend.BackendWorkflowStateMetadata) {
		m.HistoryLength--
	})

	fworkflow.WaitForTampered(t, ctx, client, id, time.Second*20)
}
