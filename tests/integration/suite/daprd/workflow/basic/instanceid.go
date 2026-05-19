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

package basic

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	fclient "github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(instanceid))
}

// instanceid verifies the workflow Start API's instance-ID validation:
//   - IDs longer than 64 characters are accepted (the historical limit was a
//     defensive guard, not a hard runtime constraint) and run end to end.
//   - IDs containing characters outside [A-Za-z0-9_-] are still rejected at
//     the API boundary, because the scheduler's reminder job-name validation
//     rejects them downstream and the API-level check gives a clearer error.
type instanceid struct {
	workflow *workflow.Workflow
}

func (i *instanceid) Setup(t *testing.T) []framework.Option {
	i.workflow = workflow.New(t,
		workflow.WithAddOrchestrator(t, "Echo", func(ctx *task.WorkflowContext) (any, error) {
			var input string
			if err := ctx.GetInput(&input); err != nil {
				return nil, err
			}
			return input, nil
		}),
	)

	return []framework.Option{
		framework.WithProcesses(i.workflow),
	}
}

func (i *instanceid) Run(t *testing.T, ctx context.Context) {
	i.workflow.WaitUntilRunning(t, ctx)

	grpcClient := i.workflow.GRPCClient(t, ctx)
	backendClient := i.workflow.BackendClient(t, ctx)
	httpClient := fclient.HTTP(t)

	longID := strings.Repeat("a", 128)

	t.Run("gRPC StartWorkflowBeta1 accepts an ID longer than the historical 64 char limit", func(t *testing.T) {
		resp, err := grpcClient.StartWorkflowBeta1(ctx, &runtimev1pb.StartWorkflowRequest{
			WorkflowComponent: "dapr",
			WorkflowName:      "Echo",
			InstanceId:        longID,
			Input:             []byte(`"hello"`),
		})
		require.NoError(t, err)
		require.Equal(t, longID, resp.GetInstanceId())

		meta, err := backendClient.WaitForWorkflowCompletion(ctx, api.InstanceID(longID), api.WithFetchPayloads(true))
		require.NoError(t, err)
		assert.True(t, api.WorkflowMetadataIsComplete(meta))
		assert.Equal(t, `"hello"`, meta.GetOutput().GetValue())
	})

	t.Run("HTTP start accepts an ID longer than the historical 64 char limit", func(t *testing.T) {
		id := longID + "_http"
		reqURL := fmt.Sprintf(
			"http://localhost:%d/v1.0-beta1/workflows/dapr/Echo/start?instanceID=%s",
			i.workflow.Dapr().HTTPPort(), url.QueryEscape(id),
		)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, strings.NewReader(`"hello"`))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")

		httpResp, err := httpClient.Do(req)
		require.NoError(t, err)
		body, err := io.ReadAll(httpResp.Body)
		require.NoError(t, httpResp.Body.Close())
		require.NoError(t, err)
		require.Equalf(t, http.StatusAccepted, httpResp.StatusCode, "body: %s", body)

		var decoded struct {
			InstanceID string `json:"instanceID"`
		}
		require.NoError(t, json.Unmarshal(body, &decoded))
		require.Equal(t, id, decoded.InstanceID)

		meta, err := backendClient.WaitForWorkflowCompletion(ctx, api.InstanceID(id), api.WithFetchPayloads(true))
		require.NoError(t, err)
		assert.True(t, api.WorkflowMetadataIsComplete(meta))
	})

	rejected := []struct {
		name       string
		instanceID string
	}{
		{name: "hash", instanceID: "domain#tenant#order-42"},
		{name: "dollar", instanceID: "order$step"},
		{name: "colon", instanceID: "order:42"},
	}

	t.Run("gRPC StartWorkflowBeta1 rejects IDs with non allowlisted characters", func(t *testing.T) {
		for _, tc := range rejected {
			t.Run(tc.name, func(t *testing.T) {
				_, err := grpcClient.StartWorkflowBeta1(ctx, &runtimev1pb.StartWorkflowRequest{
					WorkflowComponent: "dapr",
					WorkflowName:      "Echo",
					InstanceId:        tc.instanceID,
					Input:             []byte(`"hello"`),
				})
				require.Error(t, err)
				st, ok := status.FromError(err)
				require.Truef(t, ok, "expected gRPC status error, got %T: %v", err, err)
				assert.Equal(t, codes.InvalidArgument, st.Code())
				assert.Contains(t, st.Message(), tc.instanceID)
			})
		}
	})

	t.Run("HTTP start rejects IDs with non allowlisted characters", func(t *testing.T) {
		for _, tc := range rejected {
			t.Run(tc.name, func(t *testing.T) {
				reqURL := fmt.Sprintf(
					"http://localhost:%d/v1.0-beta1/workflows/dapr/Echo/start?instanceID=%s",
					i.workflow.Dapr().HTTPPort(), url.QueryEscape(tc.instanceID),
				)
				req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, strings.NewReader(`"hello"`))
				require.NoError(t, err)
				req.Header.Set("Content-Type", "application/json")

				httpResp, err := httpClient.Do(req)
				require.NoError(t, err)
				body, err := io.ReadAll(httpResp.Body)
				require.NoError(t, httpResp.Body.Close())
				require.NoError(t, err)
				require.Equalf(t, http.StatusBadRequest, httpResp.StatusCode, "body: %s", body)

				var decoded struct {
					ErrorCode string `json:"errorCode"`
					Message   string `json:"message"`
				}
				require.NoError(t, json.Unmarshal(body, &decoded))
				assert.Equal(t, "ERR_INSTANCE_ID_INVALID", decoded.ErrorCode)
				assert.Contains(t, decoded.Message, tc.instanceID)
			})
		}
	})
}
