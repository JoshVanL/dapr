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

package get

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
	suite.Register(new(get))
}

// get covers same-namespace cross-app FetchWorkflowMetadata: a workflow
// runs on app0; a client connected to app1 reads metadata via the
// new WithGetAppID option and the response reflects the instance state
// on app0.
type get struct {
	workflow *workflow.Workflow
}

func (g *get) Setup(t *testing.T) []framework.Option {
	g.workflow = workflow.New(t, workflow.WithDaprds(2))
	return []framework.Option{
		framework.WithProcesses(g.workflow),
	}
}

func (g *get) Run(t *testing.T, ctx context.Context) {
	g.workflow.WaitUntilRunning(t, ctx)

	g.workflow.Registry().AddWorkflowN("Quick", func(wctx *task.WorkflowContext) (any, error) {
		return "ok", nil
	})

	host := g.workflow.BackendClient(t, ctx)
	from := g.workflow.BackendClientN(t, ctx, 1)
	app0 := g.workflow.DaprN(0).AppID()

	id, err := host.ScheduleNewWorkflow(ctx, "Quick")
	require.NoError(t, err)

	_, err = host.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)

	meta, err := from.FetchWorkflowMetadata(ctx, id, api.WithGetAppID(app0))
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())
	assert.Equal(t, "Quick", meta.GetName())
}
