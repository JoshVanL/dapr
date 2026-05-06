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

package pauseresume

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(pauseresume))
}

// pauseresume covers same-namespace cross-app SuspendWorkflow and
// ResumeWorkflow: a workflow on app0 is suspended and resumed via a
// client connected to app1.
type pauseresume struct {
	workflow *workflow.Workflow
}

func (pr *pauseresume) Setup(t *testing.T) []framework.Option {
	pr.workflow = workflow.New(t, workflow.WithDaprds(2))
	return []framework.Option{
		framework.WithProcesses(pr.workflow),
	}
}

func (pr *pauseresume) Run(t *testing.T, ctx context.Context) {
	pr.workflow.WaitUntilRunning(t, ctx)

	pr.workflow.Registry().AddWorkflowN("WaitForEvt", func(wctx *task.WorkflowContext) (any, error) {
		return nil, wctx.WaitForSingleEvent("evt", 0).Await(nil)
	})

	host := pr.workflow.BackendClient(t, ctx)
	from := pr.workflow.BackendClientN(t, ctx, 1)
	app0 := pr.workflow.DaprN(0).AppID()

	id, err := host.ScheduleNewWorkflow(ctx, "WaitForEvt")
	require.NoError(t, err)
	_, err = host.WaitForWorkflowStart(ctx, id)
	require.NoError(t, err)

	require.NoError(t, from.SuspendWorkflow(ctx, id, "pause for ops", api.WithSuspendAppID(app0)))

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		m, ferr := host.FetchWorkflowMetadata(ctx, id)
		if !assert.NoError(c, ferr) {
			return
		}
		assert.Equal(c, api.RUNTIME_STATUS_SUSPENDED, m.GetRuntimeStatus())
	}, time.Second*30, time.Millisecond*50)

	require.NoError(t, from.ResumeWorkflow(ctx, id, "resume", api.WithResumeAppID(app0)))

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		m, ferr := host.FetchWorkflowMetadata(ctx, id)
		if !assert.NoError(c, ferr) {
			return
		}
		assert.Equal(c, api.RUNTIME_STATUS_RUNNING, m.GetRuntimeStatus())
	}, time.Second*30, time.Millisecond*50)
}
