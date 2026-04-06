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
	suite.Register(new(deny))
}

// deny tests that cross-app workflow calls are rejected when the
// WorkflowAccessPolicy explicitly denies or does not mention the caller/operation.
type deny struct {
	wf *workflow.Workflow
}

func (d *deny) Setup(t *testing.T) []framework.Option {
	sen := sentry.New(t)

	// Policy: allows "wfacl-caller" to call "AllowedWF" only.
	// "DeniedWF" is explicitly denied. "UnmentionedWF" has no rule (default deny).
	policyYAML := `
apiVersion: dapr.io/v1alpha1
kind: WorkflowAccessPolicy
metadata:
  name: deny-test
spec:
  defaultAction: deny
  rules:
  - callers:
    - appID: "wfacl-caller"
    operations:
    - type: workflow
      name: "AllowedWF"
      action: allow
    - type: workflow
      name: "DeniedWF"
      action: deny
`

	sentryOpts := daprd.WithSentry(t, sen)

	d.wf = workflow.New(t,
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
		framework.WithProcesses(sen, d.wf),
	}
}

func (d *deny) Run(t *testing.T, ctx context.Context) {
	d.wf.WaitUntilRunning(t, ctx)

	targetAppID := d.wf.DaprN(1).AppID()

	// Register orchestrators on caller. Each tries to schedule a sub-orchestrator
	// on the target app, which will be allowed or denied by the policy.
	d.wf.Registry().AddOrchestratorN("TestDeniedWF", func(ctx *task.OrchestrationContext) (any, error) {
		var output string
		err := ctx.CallSubOrchestrator("DeniedWF",
			task.WithSubOrchestratorAppID(targetAppID)).
			Await(&output)
		if err != nil {
			return nil, fmt.Errorf("sub-orchestrator failed: %w", err)
		}
		return output, nil
	})

	d.wf.Registry().AddOrchestratorN("TestUnmentionedWF", func(ctx *task.OrchestrationContext) (any, error) {
		var output string
		err := ctx.CallSubOrchestrator("UnmentionedWF",
			task.WithSubOrchestratorAppID(targetAppID)).
			Await(&output)
		if err != nil {
			return nil, fmt.Errorf("sub-orchestrator failed: %w", err)
		}
		return output, nil
	})

	// Register the target workflows (they would succeed if policy allowed them).
	d.wf.RegistryN(1).AddOrchestratorN("DeniedWF", func(ctx *task.OrchestrationContext) (any, error) {
		return "should-not-reach", nil
	})
	d.wf.RegistryN(1).AddOrchestratorN("UnmentionedWF", func(ctx *task.OrchestrationContext) (any, error) {
		return "should-not-reach", nil
	})

	client0 := d.wf.BackendClient(t, ctx)
	d.wf.BackendClientN(t, ctx, 1)

	t.Run("explicitly denied workflow fails", func(t *testing.T) {
		id, err := client0.ScheduleNewOrchestration(ctx, "TestDeniedWF")
		require.NoError(t, err)

		metadata, err := client0.WaitForOrchestrationCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		assert.True(t, api.OrchestrationMetadataIsFailed(metadata))
		assert.Contains(t, metadata.GetFailureDetails().GetErrorMessage(), "not allowed")
	})

	t.Run("unmentioned workflow fails with default deny", func(t *testing.T) {
		id, err := client0.ScheduleNewOrchestration(ctx, "TestUnmentionedWF")
		require.NoError(t, err)

		metadata, err := client0.WaitForOrchestrationCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		assert.True(t, api.OrchestrationMetadataIsFailed(metadata))
		assert.Contains(t, metadata.GetFailureDetails().GetErrorMessage(), "not allowed")
	})
}
