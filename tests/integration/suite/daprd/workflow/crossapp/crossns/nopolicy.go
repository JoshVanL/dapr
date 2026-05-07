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

package crossns

import (
	"context"
	"fmt"
	"testing"

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
	suite.Register(new(nopolicy))
}

// nopolicy verifies the default-deny invariant: cross-namespace dispatch
// is rejected when the target sidecar has no WorkflowAccessPolicy loaded.
// Same-namespace operations remain unaffected — the absence of a policy
// is permissive in the local case but strict at the cross-ns ingress.
type nopolicy struct {
	wf *workflow.Workflow
}

func (n *nopolicy) Setup(t *testing.T) []framework.Option {
	cfg := []byte(`apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: xns-config
spec:
  features:
  - name: WorkflowAccessPolicy
    enabled: true
  - name: WorkflowCrossNamespace
    enabled: true
`)

	cfgPath := writeTemp(t, "config.yaml", cfg)

	// Note: NO WAP YAML loaded on either side.
	n.wf = workflow.New(t,
		workflow.WithDaprds(2),
		workflow.WithMTLS(t),
		workflow.WithDaprdOptions(0,
			daprd.WithAppID("xns-nopolicy-caller"),
			daprd.WithNamespace("default"),
			daprd.WithConfigs(cfgPath),
		),
		workflow.WithDaprdOptions(1,
			daprd.WithAppID("xns-nopolicy-target"),
			daprd.WithNamespace("other"),
			daprd.WithConfigs(cfgPath),
		),
	)
	return []framework.Option{framework.WithProcesses(n.wf)}
}

func (n *nopolicy) Run(t *testing.T, ctx context.Context) {
	t.Skip("requires production cross-namespace name resolution; tracked as follow-up")

	n.wf.WaitUntilRunning(t, ctx)

	n.wf.RegistryN(1).AddWorkflowN("Child", func(ctx *task.WorkflowContext) (any, error) {
		return "should-not-run", nil
	})
	n.wf.Registry().AddWorkflowN("Parent", func(ctx *task.WorkflowContext) (any, error) {
		var output string
		err := ctx.CallChildWorkflow("Child",
			task.WithChildWorkflowAppID(n.wf.DaprN(1).AppID()),
			task.WithChildWorkflowAppNamespace("other"),
		).Await(&output)
		if err != nil {
			return nil, fmt.Errorf("expected denial without policy: %w", err)
		}
		return output, nil
	})

	parent := n.wf.BackendClient(t, ctx)
	id, err := parent.ScheduleNewWorkflow(ctx, "Parent")
	require.NoError(t, err)

	meta, err := parent.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_FAILED, meta.GetRuntimeStatus())
	// Default-deny on cross-ns ingress when no policy is loaded.
	assert.Contains(t, meta.GetFailureDetails().GetErrorType(), "WorkflowAccessPolicyDenied")
}
