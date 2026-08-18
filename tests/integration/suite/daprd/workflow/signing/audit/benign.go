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
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	dworkflow "github.com/dapr/durabletask-go/workflow"
)

func init() {
	suite.Register(new(benign))
}

// benign verifies the background integrity audit produces no false
// tombstones: with a very aggressive audit interval racing live turns, a
// workflow that runs activities, continues-as-new, and waits on an external
// event still completes normally.
type benign struct {
	workflow *workflow.Workflow
}

func (b *benign) Setup(t *testing.T) []framework.Option {
	b.workflow = workflow.New(t,
		workflow.WithMTLS(t),
		workflow.WithSigningAuditInterval("200ms"),
	)

	return []framework.Option{
		framework.WithProcesses(b.workflow),
	}
}

func (b *benign) Run(t *testing.T, ctx context.Context) {
	b.workflow.WaitUntilRunning(t, ctx)

	var counter atomic.Int32

	reg := dworkflow.NewRegistry()
	reg.AddWorkflowN("audit-benign", func(ctx *dworkflow.WorkflowContext) (any, error) {
		var iteration int
		if err := ctx.GetInput(&iteration); err != nil {
			return nil, err
		}

		for range 3 {
			if err := ctx.CallActivity("counter").Await(nil); err != nil {
				return nil, err
			}
		}

		if iteration < 2 {
			ctx.ContinueAsNew(iteration + 1)
			return nil, nil
		}

		var payload string
		if err := ctx.WaitForExternalEvent("continue", time.Minute*5).Await(&payload); err != nil {
			return nil, err
		}
		return payload, nil
	})
	reg.AddActivityN("counter", func(ctx dworkflow.ActivityContext) (any, error) {
		return counter.Add(1), nil
	})

	client := b.workflow.WorkflowClient(t, ctx)
	require.NoError(t, client.StartWorker(ctx, reg))

	id, err := client.ScheduleWorkflow(ctx, "audit-benign", dworkflow.WithInput(0))
	require.NoError(t, err)

	// Let the audit sweep the parked workflow a few times before completing
	// it, so idle-resident audits are exercised as well as turn-racing ones.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		wmeta, werr := client.FetchWorkflowMetadata(ctx, id)
		assert.NoError(c, werr)
		if !assert.NotNil(c, wmeta) {
			return
		}
		assert.Equal(c, dworkflow.StatusRunning, wmeta.RuntimeStatus)
		assert.Equal(c, int32(9), counter.Load())
	}, time.Second*20, time.Millisecond*10)

	time.Sleep(time.Second)

	require.NoError(t, client.RaiseEvent(ctx, id, "continue", dworkflow.WithEventPayload("done")))

	meta, err := client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, dworkflow.StatusCompleted, meta.RuntimeStatus)
	assert.Equal(t, int32(9), counter.Load())
}
