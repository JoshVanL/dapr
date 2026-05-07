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
	suite.Register(new(deny))
}

// deny exercises the policy-rejection path. The target sidecar has a
// WorkflowAccessPolicy that does NOT include the caller's (app, namespace).
// The cross-ns dispatch reminder fires, the ingress returns
// PermissionDenied, the caller-side handler synthesises a
// ChildWorkflowInstanceFailed event, and the parent workflow surfaces
// the failure.
type deny struct {
	wf *workflow.Workflow
}

func (d *deny) Setup(t *testing.T) []framework.Option {
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

	// Target-side policy: only allow a DIFFERENT app, so the actual
	// caller xns-deny-caller@default is rejected.
	targetPolicy := []byte(`apiVersion: dapr.io/v1alpha1
kind: WorkflowAccessPolicy
metadata:
  name: xns-deny-target
  namespace: other
spec:
  rules:
  - callers:
    - appID: not-the-caller
      namespace: default
    workflows:
    - name: "*"
      operations: [schedule]
`)

	cfgPath := writeTemp(t, "config.yaml", cfg)
	targetPolicyPath := writeTemp(t, "target-policy.yaml", targetPolicy)

	d.wf = workflow.New(t,
		workflow.WithDaprds(2),
		workflow.WithMTLS(t),
		workflow.WithDaprdOptions(0,
			daprd.WithAppID("xns-deny-caller"),
			daprd.WithNamespace("default"),
			daprd.WithConfigs(cfgPath),
		),
		workflow.WithDaprdOptions(1,
			daprd.WithAppID("xns-deny-target"),
			daprd.WithNamespace("other"),
			daprd.WithConfigs(cfgPath),
			daprd.WithResourceFiles(targetPolicyPath),
		),
	)
	return []framework.Option{framework.WithProcesses(d.wf)}
}

func (d *deny) Run(t *testing.T, ctx context.Context) {
	t.Skip("requires production cross-namespace name resolution; tracked as follow-up")

	d.wf.WaitUntilRunning(t, ctx)

	d.wf.RegistryN(1).AddWorkflowN("Child", func(ctx *task.WorkflowContext) (any, error) {
		return "should-not-run", nil
	})
	d.wf.Registry().AddWorkflowN("Parent", func(ctx *task.WorkflowContext) (any, error) {
		var output string
		err := ctx.CallChildWorkflow("Child",
			task.WithChildWorkflowAppID(d.wf.DaprN(1).AppID()),
			task.WithChildWorkflowAppNamespace("other"),
		).Await(&output)
		if err != nil {
			return nil, fmt.Errorf("expected denial: %w", err)
		}
		return output, nil
	})

	parent := d.wf.BackendClient(t, ctx)
	id, err := parent.ScheduleNewWorkflow(ctx, "Parent")
	require.NoError(t, err)

	meta, err := parent.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)
	// Parent workflow itself surfaces the child failure as its own failure.
	assert.Equal(t, api.RUNTIME_STATUS_FAILED, meta.GetRuntimeStatus())
	// The synthesised failure carries the WorkflowAccessPolicyDenied error type.
	assert.Contains(t, meta.GetFailureDetails().GetErrorType(), "WorkflowAccessPolicyDenied")
	_ = time.Second
}
