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
	suite.Register(new(specificity))
}

// specificity tests the most-specific-rule-wins behavior end-to-end.
// Policy: deny *, allow Process*, deny ProcessSecret.
type specificity struct {
	wf *workflow.Workflow
}

func (s *specificity) Setup(t *testing.T) []framework.Option {
	sen := sentry.New(t)

	policyYAML := `
apiVersion: dapr.io/v1alpha1
kind: WorkflowAccessPolicy
metadata:
  name: specificity-test
spec:
  defaultAction: deny
  rules:
  - callers:
    - appID: "spec-caller"
    operations:
    - type: workflow
      name: "*"
      action: deny
    - type: workflow
      name: "Process*"
      action: allow
    - type: workflow
      name: "ProcessSecret"
      action: deny
`

	sentryOpts := daprd.WithSentry(t, sen)

	s.wf = workflow.New(t,
		workflow.WithDaprds(2),
		workflow.WithDaprdOptions(0,
			daprd.WithAppID("spec-caller"),
			daprd.WithConfigManifests(t, configWithFeatureFlag()),
			sentryOpts,
		),
		workflow.WithDaprdOptions(1,
			daprd.WithAppID("spec-target"),
			daprd.WithConfigManifests(t, configWithFeatureFlag()),
			daprd.WithResourceFiles(policyYAML),
			sentryOpts,
		),
	)

	return []framework.Option{
		framework.WithProcesses(sen, s.wf),
	}
}

func (s *specificity) Run(t *testing.T, ctx context.Context) {
	s.wf.WaitUntilRunning(t, ctx)

	targetAppID := s.wf.DaprN(1).AppID()

	// Caller orchestrators that schedule sub-orchestrators on target.
	for _, wfName := range []string{"ProcessOrder", "ProcessSecret", "CancelOrder"} {
		name := wfName
		s.wf.Registry().AddOrchestratorN("Test_"+name, func(ctx *task.OrchestrationContext) (any, error) {
			var output string
			err := ctx.CallSubOrchestrator(name,
				task.WithSubOrchestratorAppID(targetAppID)).
				Await(&output)
			if err != nil {
				return nil, fmt.Errorf("sub-orchestrator %s failed: %w", name, err)
			}
			return output, nil
		})
	}

	// Target orchestrators.
	for _, wfName := range []string{"ProcessOrder", "ProcessSecret", "CancelOrder"} {
		name := wfName
		s.wf.RegistryN(1).AddOrchestratorN(name, func(ctx *task.OrchestrationContext) (any, error) {
			return "completed-" + name, nil
		})
	}

	client0 := s.wf.BackendClient(t, ctx)
	s.wf.BackendClientN(t, ctx, 1)

	t.Run("ProcessOrder allowed (Process* matches, more specific than *)", func(t *testing.T) {
		id, err := client0.ScheduleNewOrchestration(ctx, "Test_ProcessOrder")
		require.NoError(t, err)
		metadata, err := client0.WaitForOrchestrationCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		assert.True(t, api.OrchestrationMetadataIsComplete(metadata))
	})

	t.Run("ProcessSecret denied (exact deny beats Process* allow)", func(t *testing.T) {
		id, err := client0.ScheduleNewOrchestration(ctx, "Test_ProcessSecret")
		require.NoError(t, err)
		metadata, err := client0.WaitForOrchestrationCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		assert.True(t, api.OrchestrationMetadataIsFailed(metadata))
		assert.Contains(t, metadata.GetFailureDetails().GetErrorMessage(), "not allowed")
	})

	t.Run("CancelOrder denied (only * matches, which is deny)", func(t *testing.T) {
		id, err := client0.ScheduleNewOrchestration(ctx, "Test_CancelOrder")
		require.NoError(t, err)
		metadata, err := client0.WaitForOrchestrationCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		assert.True(t, api.OrchestrationMetadataIsFailed(metadata))
		assert.Contains(t, metadata.GetFailureDetails().GetErrorMessage(), "not allowed")
	})
}
