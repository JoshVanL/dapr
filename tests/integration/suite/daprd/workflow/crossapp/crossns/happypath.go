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

// Package crossns covers the cross-namespace workflow bridge end-to-end.
package crossns

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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
	suite.Register(new(happypath))
}

// happypath drives a parent workflow in namespace "default" that calls a
// child workflow in namespace "other" via the cross-namespace bridge.
// Both daprds run with WorkflowCrossNamespace + WorkflowAccessPolicy
// enabled and have policies allowing the dispatch and the result hop.
type happypath struct {
	wf *workflow.Workflow
}

func (h *happypath) Setup(t *testing.T) []framework.Option {
	const (
		callerAppID = "xns-caller"
		callerNS    = "default"
		targetAppID = "xns-target"
		targetNS    = "other"
	)

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

	// Target-side policy: allow the caller (default/xns-caller) to
	// schedule any workflow on this target.
	targetPolicy := []byte(`apiVersion: dapr.io/v1alpha1
kind: WorkflowAccessPolicy
metadata:
  name: xns-target-policy
  namespace: other
spec:
  rules:
  - callers:
    - appID: xns-caller
      namespace: default
    workflows:
    - name: "*"
      operations: [schedule]
`)

	// Caller-side policy: allow the responding child app
	// (other/xns-target) to deliver results back into any workflow.
	callerPolicy := []byte(`apiVersion: dapr.io/v1alpha1
kind: WorkflowAccessPolicy
metadata:
  name: xns-caller-policy
  namespace: default
spec:
  rules:
  - callers:
    - appID: xns-target
      namespace: other
    workflows:
    - name: "*"
      operations: [schedule]
`)

	cfgPath := writeTemp(t, "config.yaml", cfg)
	targetPolicyPath := writeTemp(t, "target-policy.yaml", targetPolicy)
	callerPolicyPath := writeTemp(t, "caller-policy.yaml", callerPolicy)

	h.wf = workflow.New(t,
		workflow.WithDaprds(2),
		workflow.WithMTLS(t),
		workflow.WithDaprdOptions(0,
			daprd.WithAppID(callerAppID),
			daprd.WithNamespace(callerNS),
			daprd.WithConfigs(cfgPath),
			daprd.WithResourceFiles(callerPolicyPath),
		),
		workflow.WithDaprdOptions(1,
			daprd.WithAppID(targetAppID),
			daprd.WithNamespace(targetNS),
			daprd.WithConfigs(cfgPath),
			daprd.WithResourceFiles(targetPolicyPath),
		),
	)
	return []framework.Option{framework.WithProcesses(h.wf)}
}

func (h *happypath) Run(t *testing.T, ctx context.Context) {
	t.Skip("requires production cross-namespace name resolution + sentry-issued SPIFFE identities; tracked as follow-up to wire the standalone-mode resolver for cross-ns sidecar discovery")

	h.wf.WaitUntilRunning(t, ctx)

	// Child runs on app1 (other namespace).
	h.wf.RegistryN(1).AddWorkflowN("Child", func(ctx *task.WorkflowContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, fmt.Errorf("child input: %w", err)
		}
		return "child-saw: " + input, nil
	})

	// Parent runs on app0 (default namespace) and calls the child cross-ns.
	h.wf.Registry().AddWorkflowN("Parent", func(ctx *task.WorkflowContext) (any, error) {
		var output string
		err := ctx.CallChildWorkflow("Child",
			task.WithChildWorkflowInput("from-default"),
			task.WithChildWorkflowAppID(h.wf.DaprN(1).AppID()),
			task.WithChildWorkflowAppNamespace("other"),
		).Await(&output)
		if err != nil {
			return nil, fmt.Errorf("cross-ns child failed: %w", err)
		}
		return output, nil
	})

	parent := h.wf.BackendClient(t, ctx)

	id, err := parent.ScheduleNewWorkflow(ctx, "Parent")
	require.NoError(t, err)

	meta, err := parent.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)
	assert.True(t, api.WorkflowMetadataIsComplete(meta))
	assert.JSONEq(t, `"child-saw: from-default"`, meta.GetOutput().GetValue())
}

func writeTemp(t *testing.T, name string, body []byte) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, body, 0o600))
	return path
}
