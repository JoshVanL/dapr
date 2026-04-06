//go:build e2e
// +build e2e

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

package workflow_accesspolicy_e2e

import (
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/e2e/utils"
	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/runner"
)

var tr *runner.TestRunner

func TestMain(m *testing.M) {
	utils.SetupLogs("workflow_accesspolicy")
	utils.InitHTTPClient(true)

	testApps := []kube.AppDescription{
		{
			AppName:             "wfacl-caller",
			DaprEnabled:         true,
			ImageName:           "e2e-workflowsapp",
			Replicas:            1,
			IngressEnabled:      true,
			IngressPort:         3000,
			DaprMemoryLimit:     "200Mi",
			DaprMemoryRequest:   "100Mi",
			AppMemoryLimit:      "200Mi",
			AppMemoryRequest:    "100Mi",
			AppPort:             -1,
			DebugLoggingEnabled: true,
			Config:              "wfaclconfig",
		},
		{
			AppName:             "wfacl-target",
			DaprEnabled:         true,
			ImageName:           "e2e-workflowsapp",
			Replicas:            1,
			IngressEnabled:      true,
			IngressPort:         3000,
			DaprMemoryLimit:     "200Mi",
			DaprMemoryRequest:   "100Mi",
			AppMemoryLimit:      "200Mi",
			AppMemoryRequest:    "100Mi",
			AppPort:             -1,
			DebugLoggingEnabled: true,
			Config:              "wfaclconfig",
		},
	}

	// The following Kubernetes resources must be applied before running:
	// 1. Configuration "wfaclconfig" with WorkflowAccessPolicy feature enabled
	// 2. WorkflowAccessPolicy allowing "wfacl-caller" to schedule "AllowedWorkflow"
	//    and denying "DeniedWorkflow" on "wfacl-target"
	tr = runner.NewTestRunner("workflow_accesspolicy", testApps, nil, nil)
	os.Exit(tr.Start(m))
}

func TestWorkflowAccessPolicy(t *testing.T) {
	callerURL := tr.Platform.AcquireAppExternalURL("wfacl-caller")
	require.NotEmpty(t, callerURL, "wfacl-caller external URL must not be empty")

	targetURL := tr.Platform.AcquireAppExternalURL("wfacl-target")
	require.NotEmpty(t, targetURL, "wfacl-target external URL must not be empty")

	// Wait for apps to be healthy.
	require.NoError(t, utils.HealthCheckApps(callerURL, targetURL))

	t.Run("allowed workflow succeeds", func(t *testing.T) {
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			resp, err := utils.HTTPPost(fmt.Sprintf("%s/start-workflow/AllowedWorkflow", callerURL), nil)
			assert.NoError(c, err)
			assert.Equal(c, http.StatusOK, resp.StatusCode)
		}, 30*time.Second, time.Second)
	})

	t.Run("denied workflow fails", func(t *testing.T) {
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			resp, err := utils.HTTPPost(fmt.Sprintf("%s/start-workflow/DeniedWorkflow", callerURL), nil)
			assert.NoError(c, err)
			// Expect a non-200 status indicating the workflow was denied.
			assert.NotEqual(c, http.StatusOK, resp.StatusCode)
		}, 30*time.Second, time.Second)
	})

	t.Run("unmentioned workflow fails with default deny", func(t *testing.T) {
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			resp, err := utils.HTTPPost(fmt.Sprintf("%s/start-workflow/UnmentionedWorkflow", callerURL), nil)
			assert.NoError(c, err)
			assert.NotEqual(c, http.StatusOK, resp.StatusCode)
		}, 30*time.Second, time.Second)
	})
}
