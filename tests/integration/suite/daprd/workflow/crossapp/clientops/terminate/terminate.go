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

package terminate

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
	suite.Register(new(terminate))
}

// terminate covers same-namespace cross-app TerminateWorkflow via the
// durabletask client: a workflow runs on app0, a client connected to app1
// calls TerminateWorkflow with WithTerminateAppID(app0), and the instance
// transitions to TERMINATED on app0.
type terminate struct {
	workflow *workflow.Workflow
}

func (te *terminate) Setup(t *testing.T) []framework.Option {
	te.workflow = workflow.New(t, workflow.WithDaprds(2))
	return []framework.Option{
		framework.WithProcesses(te.workflow),
	}
}

func (te *terminate) Run(t *testing.T, ctx context.Context) {
	te.workflow.WaitUntilRunning(t, ctx)

	// Long-running workflow on app0 so the terminate has something to act on.
	te.workflow.Registry().AddWorkflowN("LongRunner", func(wctx *task.WorkflowContext) (any, error) {
		if err := wctx.CreateTimer(time.Hour).Await(nil); err != nil {
			return nil, err
		}
		return "done", nil
	})

	host := te.workflow.BackendClient(t, ctx) // app0
	from := te.workflow.BackendClientN(t, ctx, 1) // app1
	app0 := te.workflow.DaprN(0).AppID()

	id, err := host.ScheduleNewWorkflow(ctx, "LongRunner")
	require.NoError(t, err)

	_, err = host.WaitForWorkflowStart(ctx, id)
	require.NoError(t, err)

	// Cross-app terminate from app1, targeting app0.
	require.NoError(t, from.TerminateWorkflow(ctx, id, api.WithTerminateAppID(app0)))

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		meta, ferr := host.FetchWorkflowMetadata(ctx, id)
		if !assert.NoError(c, ferr) {
			return
		}
		assert.Equal(c, api.RUNTIME_STATUS_TERMINATED, meta.GetRuntimeStatus())
	}, time.Second*30, time.Millisecond*50)
}
