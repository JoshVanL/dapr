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

package schedule

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
	suite.Register(new(schedule))
}

// schedule covers same-namespace cross-app StartWorkflow via the
// durabletask client: a client connected to app1 schedules a workflow
// targeting app0 with WithStartAppID, then verifies the instance runs on
// app0.
type schedule struct {
	workflow *workflow.Workflow
}

func (s *schedule) Setup(t *testing.T) []framework.Option {
	s.workflow = workflow.New(t, workflow.WithDaprds(2))
	return []framework.Option{
		framework.WithProcesses(s.workflow),
	}
}

func (s *schedule) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)

	// Workflow registered only on app0; if the schedule does not route
	// cross-app the instance would either fail or never start.
	s.workflow.Registry().AddWorkflowN("Greet", func(wctx *task.WorkflowContext) (any, error) {
		var name string
		if err := wctx.GetInput(&name); err != nil {
			return nil, err
		}
		return "hello " + name, nil
	})

	// Drive the schedule from app1's client targeting app0.
	app0 := s.workflow.DaprN(0).AppID()
	from := s.workflow.BackendClientN(t, ctx, 1)

	id, err := from.ScheduleNewWorkflow(ctx, "Greet",
		api.WithInput("world"),
		api.WithStartAppID(app0),
	)
	require.NoError(t, err)

	// Read back via app0's client (the one that hosts the instance).
	host := s.workflow.BackendClient(t, ctx)
	meta, err := host.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())
	assert.JSONEq(t, `"hello world"`, meta.GetOutput().GetValue())
}
