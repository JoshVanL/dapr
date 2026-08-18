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
	dworkflow "github.com/dapr/durabletask-go/workflow"
)

func init() {
	suite.Register(new(verified))
}

// verified asserts the audit actually sweeps resident actors on its
// interval: an untampered parked workflow accumulates verified audit metrics
// over time, is never tombstoned, and still completes normally afterwards.
type verified struct {
	workflow *workflow.Workflow
}

func (v *verified) Setup(t *testing.T) []framework.Option {
	v.workflow = workflow.New(t,
		workflow.WithMTLS(t),
		workflow.WithSigningAuditInterval("500ms"),
	)

	return []framework.Option{
		framework.WithProcesses(v.workflow),
	}
}

func (v *verified) Run(t *testing.T, ctx context.Context) {
	v.workflow.WaitUntilRunning(t, ctx)

	client := v.workflow.WorkflowClient(t, ctx)
	id := fworkflow.StartParkedWorkflow(t, ctx, client, fworkflow.ParkedRegistry("audit-verified"), "audit-verified")

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, fworkflow.IntegrityAuditCount(c, ctx, v.workflow.Dapr(), "verified"), 2.0)
	}, time.Second*20, time.Millisecond*100)

	assert.Zero(t, fworkflow.IntegrityAuditCount(t, ctx, v.workflow.Dapr(), "tampered"))
	assert.Zero(t, fworkflow.IntegrityAuditCount(t, ctx, v.workflow.Dapr(), "divergent"))

	require.NoError(t, client.RaiseEvent(ctx, id, "continue", dworkflow.WithEventPayload("done")))

	meta, err := client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, dworkflow.StatusCompleted, meta.RuntimeStatus)
}
