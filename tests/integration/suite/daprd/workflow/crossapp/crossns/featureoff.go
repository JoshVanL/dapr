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
	suite.Register(new(featureoff))
}

// featureoff verifies the WorkflowCrossNamespace feature-flag gate. With
// the flag disabled on the target sidecar, the cross-ns ingress returns
// Unimplemented; the caller-side handler classifies that as terminal,
// calls failXNSDispatch with CrossNamespaceUnsupported, and the parent
// workflow surfaces the failure.
type featureoff struct {
	wf *workflow.Workflow
}

func (f *featureoff) Setup(t *testing.T) []framework.Option {
	// Caller has the feature ON so dispatch attempts will fire; target
	// has it OFF so the ingress refuses with Unimplemented.
	callerCfg := []byte(`apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: xns-config-on
spec:
  features:
  - name: WorkflowAccessPolicy
    enabled: true
  - name: WorkflowCrossNamespace
    enabled: true
`)
	targetCfg := []byte(`apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: xns-config-off
spec:
  features:
  - name: WorkflowAccessPolicy
    enabled: true
`)

	callerCfgPath := writeTemp(t, "caller-config.yaml", callerCfg)
	targetCfgPath := writeTemp(t, "target-config.yaml", targetCfg)

	f.wf = workflow.New(t,
		workflow.WithDaprds(2),
		workflow.WithMTLS(t),
		workflow.WithDaprdOptions(0,
			daprd.WithAppID("xns-featureoff-caller"),
			daprd.WithNamespace("default"),
			daprd.WithConfigs(callerCfgPath),
		),
		workflow.WithDaprdOptions(1,
			daprd.WithAppID("xns-featureoff-target"),
			daprd.WithNamespace("other"),
			daprd.WithConfigs(targetCfgPath),
		),
	)
	return []framework.Option{framework.WithProcesses(f.wf)}
}

func (f *featureoff) Run(t *testing.T, ctx context.Context) {
	t.Skip("requires production cross-namespace name resolution; tracked as follow-up")

	f.wf.WaitUntilRunning(t, ctx)

	f.wf.RegistryN(1).AddWorkflowN("Child", func(ctx *task.WorkflowContext) (any, error) {
		return "should-not-run", nil
	})
	f.wf.Registry().AddWorkflowN("Parent", func(ctx *task.WorkflowContext) (any, error) {
		var output string
		err := ctx.CallChildWorkflow("Child",
			task.WithChildWorkflowAppID(f.wf.DaprN(1).AppID()),
			task.WithChildWorkflowAppNamespace("other"),
		).Await(&output)
		if err != nil {
			return nil, fmt.Errorf("expected feature-off failure: %w", err)
		}
		return output, nil
	})

	parent := f.wf.BackendClient(t, ctx)
	id, err := parent.ScheduleNewWorkflow(ctx, "Parent")
	require.NoError(t, err)

	meta, err := parent.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_FAILED, meta.GetRuntimeStatus())
	assert.Contains(t, meta.GetFailureDetails().GetErrorType(), "CrossNamespaceUnsupported")
}
