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

package accesspolicy

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(allow))
}

// allow tests that cross-app workflow and activity calls succeed when the
// WorkflowAccessPolicy explicitly allows the caller.
type allow struct {
	wf *workflow.Workflow
}

func (a *allow) Setup(t *testing.T) []framework.Option {
	sen := sentry.New(t)

	policyYAML := `
apiVersion: dapr.io/v1alpha1
kind: WorkflowAccessPolicy
metadata:
  name: allow-test
spec:
  defaultAction: deny
  rules:
  - callers:
    - appID: "wfacl-caller"
    operations:
    - type: workflow
      name: "AllowedWorkflow"
      action: allow
    - type: activity
      name: "AllowedActivity"
      action: allow
`

	sentryOpts := daprd.WithSentry(t, sen)

	a.wf = workflow.New(t,
		workflow.WithDaprds(2),
		workflow.WithDaprdOptions(0,
			daprd.WithAppID("wfacl-caller"),
			daprd.WithConfigManifests(t, configWithFeatureFlag()),
			sentryOpts,
		),
		workflow.WithDaprdOptions(1,
			daprd.WithAppID("wfacl-target"),
			daprd.WithConfigManifests(t, configWithFeatureFlag()),
			daprd.WithResourceFiles(policyYAML),
			sentryOpts,
		),
	)

	return []framework.Option{
		framework.WithProcesses(sen, a.wf),
	}
}

func (a *allow) Run(t *testing.T, ctx context.Context) {
	a.wf.WaitUntilRunning(t, ctx)

	// Register orchestrator on caller (daprd0) that calls activity on target (daprd1).
	a.wf.Registry().AddOrchestratorN("AllowedWorkflow", func(ctx *task.OrchestrationContext) (any, error) {
		var output string
		err := ctx.CallActivity("AllowedActivity",
			task.WithActivityAppID(a.wf.DaprN(1).AppID())).
			Await(&output)
		if err != nil {
			return nil, fmt.Errorf("activity failed: %w", err)
		}
		return output, nil
	})

	// Register activity on target (daprd1).
	a.wf.RegistryN(1).AddActivityN("AllowedActivity", func(ctx task.ActivityContext) (any, error) {
		return "allowed-result", nil
	})

	client0 := a.wf.BackendClient(t, ctx)
	a.wf.BackendClientN(t, ctx, 1)

	t.Run("allowed cross-app workflow with activity succeeds", func(t *testing.T) {
		id, err := client0.ScheduleNewOrchestration(ctx, "AllowedWorkflow")
		require.NoError(t, err)

		metadata, err := client0.WaitForOrchestrationCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		assert.True(t, api.OrchestrationMetadataIsComplete(metadata))
		assert.Equal(t, `"allowed-result"`, metadata.GetOutput().GetValue())
	})
}
