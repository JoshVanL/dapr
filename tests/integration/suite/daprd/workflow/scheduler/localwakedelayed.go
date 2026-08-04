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

package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(localwakedelayed))
}

// localwakedelayed pins the delayed-start scoping of the local wake fast
// path: a workflow scheduled with a future start time must keep its scheduler
// due time and NOT be executed early by the fast path.
type localwakedelayed struct {
	workflow *workflow.Workflow
}

func (l *localwakedelayed) Setup(t *testing.T) []framework.Option {
	l.workflow = workflow.New(t,
		workflow.WithDaprdOptions(0, daprd.WithConfigManifests(t, localWakeFeatureConfig)),
	)

	return []framework.Option{
		framework.WithProcesses(l.workflow),
	}
}

func (l *localwakedelayed) Run(t *testing.T, ctx context.Context) {
	l.workflow.WaitUntilRunning(t, ctx)

	var executed time.Time
	l.workflow.Registry().AddWorkflowN("delay", func(ctx *task.WorkflowContext) (any, error) {
		if !ctx.IsReplaying {
			executed = time.Now()
		}
		return nil, nil
	})

	client := l.workflow.BackendClient(t, ctx)

	start := time.Now()
	id, err := client.ScheduleNewWorkflow(ctx, "delay", api.WithStartTime(start.Add(time.Second*5)))
	require.NoError(t, err)
	_, err = client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.InDelta(t, 5.0, executed.Sub(start).Seconds(), 2.0,
		"the fast path must not run a delayed start early; the scheduler fires it at its due time")
}
