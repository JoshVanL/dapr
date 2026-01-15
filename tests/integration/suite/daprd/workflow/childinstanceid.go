/*
Copyright 2026 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://wwb.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package workflow

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	dworkflow "github.com/dapr/durabletask-go/workflow"
)

func init() {
	suite.Register(new(childinstanceid))
}

type childinstanceid struct {
	workflow *workflow.Workflow
}

func (c *childinstanceid) Setup(t *testing.T) []framework.Option {
	c.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(c.workflow),
	}
}

func (c *childinstanceid) Run(t *testing.T, ctx context.Context) {
	c.workflow.WaitUntilRunning(t, ctx)

	reg := dworkflow.NewRegistry()
	reg.AddWorkflowN("foo", func(ctx *dworkflow.WorkflowContext) (any, error) {
		require.NoError(t, ctx.CallChildWorkflow("bar", dworkflow.WithChildWorkflowInstanceID("xyz")).Await(nil))
		require.NoError(t, ctx.CallChildWorkflow("bar", dworkflow.WithChildWorkflowInstanceID("xyz")).Await(nil))
		return nil, nil
	})
	reg.AddWorkflowN("bar", func(ctx *dworkflow.WorkflowContext) (any, error) {
		fmt.Printf(">>HERE\n")
		time.Sleep(time.Second * 5)
		return nil, nil
	})

	wf := c.workflow.WorkflowClient(t, ctx)
	wf.StartWorker(ctx, reg)

	id1, err := wf.ScheduleWorkflow(ctx, "foo", dworkflow.WithInstanceID("abc"))
	require.NoError(t, err)
	id2, err := wf.ScheduleWorkflow(ctx, "foo", dworkflow.WithInstanceID("abc2"))
	require.NoError(t, err)
	_, err = wf.WaitForWorkflowCompletion(ctx, id1)
	require.NoError(t, err)
	_, err = wf.WaitForWorkflowCompletion(ctx, id2)
	require.NoError(t, err)

	ids, err := wf.ListInstanceIDs(ctx)
	require.NoError(t, err)
	for _, id := range ids.InstanceIds {
		fmt.Printf(">>%s\n", id)
	}
}
