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

package raiseevent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(raiseevent))
}

// raiseevent covers same-namespace cross-app RaiseEvent: a workflow on
// app0 waits for an external event; a client connected to app1 raises
// the event with WithRaiseEventAppID(app0); the workflow completes with
// the supplied payload.
type raiseevent struct {
	workflow *workflow.Workflow
}

func (re *raiseevent) Setup(t *testing.T) []framework.Option {
	re.workflow = workflow.New(t, workflow.WithDaprds(2))
	return []framework.Option{
		framework.WithProcesses(re.workflow),
	}
}

func (re *raiseevent) Run(t *testing.T, ctx context.Context) {
	re.workflow.WaitUntilRunning(t, ctx)

	re.workflow.Registry().AddWorkflowN("WaitForGo", func(wctx *task.WorkflowContext) (any, error) {
		var payload string
		if err := wctx.WaitForSingleEvent("go", 0).Await(&payload); err != nil {
			return nil, err
		}
		return payload, nil
	})

	host := re.workflow.BackendClient(t, ctx)
	from := re.workflow.BackendClientN(t, ctx, 1)
	app0 := re.workflow.DaprN(0).AppID()

	id, err := host.ScheduleNewWorkflow(ctx, "WaitForGo")
	require.NoError(t, err)

	_, err = host.WaitForWorkflowStart(ctx, id)
	require.NoError(t, err)

	require.NoError(t, from.RaiseEvent(ctx, id, "go",
		api.WithEventPayload("ack"),
		api.WithRaiseEventAppID(app0),
	))

	meta, err := host.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())
	assert.JSONEq(t, `"ack"`, meta.GetOutput().GetValue())
}
