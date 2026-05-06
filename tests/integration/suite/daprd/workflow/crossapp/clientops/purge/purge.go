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

package purge

import (
	"context"
	"errors"
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
	suite.Register(new(purge))
}

// purge covers same-namespace cross-app PurgeWorkflowState: a workflow
// runs to completion on app0; a client connected to app1 issues
// PurgeWorkflowState with WithPurgeAppID(app0), and the metadata
// disappears on app0.
type purge struct {
	workflow *workflow.Workflow
}

func (p *purge) Setup(t *testing.T) []framework.Option {
	p.workflow = workflow.New(t, workflow.WithDaprds(2))
	return []framework.Option{
		framework.WithProcesses(p.workflow),
	}
}

func (p *purge) Run(t *testing.T, ctx context.Context) {
	p.workflow.WaitUntilRunning(t, ctx)

	p.workflow.Registry().AddWorkflowN("Quick", func(wctx *task.WorkflowContext) (any, error) {
		return "ok", nil
	})

	host := p.workflow.BackendClient(t, ctx)
	from := p.workflow.BackendClientN(t, ctx, 1)
	app0 := p.workflow.DaprN(0).AppID()

	id, err := host.ScheduleNewWorkflow(ctx, "Quick")
	require.NoError(t, err)

	_, err = host.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)

	require.NoError(t, from.PurgeWorkflowState(ctx, id, api.WithPurgeAppID(app0)))

	_, err = host.FetchWorkflowMetadata(ctx, id)
	assert.True(t, errors.Is(err, api.ErrInstanceNotFound) || err != nil, "expected purge to remove instance metadata, got: %v", err)
}
