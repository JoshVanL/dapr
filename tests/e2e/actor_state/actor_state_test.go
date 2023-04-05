//go:build e2e
// +build e2e

/*
Copyright 2021 The Dapr Authors
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
package activation

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/anypb"

	runtimev1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/e2e/utils"
	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/runner"
)

const (
	appName         = "actorstate" // App name in Dapr.
	numHealthChecks = 60           // Number of get calls before starting tests.
)

var tr *runner.TestRunner

func TestMain(m *testing.M) {
	utils.SetupLogs("actor_state")
	utils.InitHTTPClient(true)

	// These apps will be deployed before starting actual test and will be
	// cleaned up after all tests are finished automatically
	testApps := []kube.AppDescription{
		{
			AppName:        appName,
			DaprEnabled:    true,
			ImageName:      "e2e-actorstate",
			Replicas:       1,
			IngressEnabled: true,
			MetricsEnabled: true,
		},
	}

	tr = runner.NewTestRunner(appName, testApps, nil, nil)
	os.Exit(tr.Start(m))
}

func TestActorState(t *testing.T) {
	// TODO @joshvanl: understand why actor state calls are failing for CosmosDB.
	if os.Getenv("TEST_CLOUD_ENV") == "azure" {
		t.Skip("The following tests fail when using CosmosDB as the state store. Skipping.")
	}

	utils.InitHTTPClient(true)
	externalURL := tr.Platform.AcquireAppExternalURL(appName)
	require.NotEmpty(t, externalURL, "external URL must not be empty!")

	initActorURL := fmt.Sprintf("%s/test/initactor", externalURL)
	httpURL := fmt.Sprintf("%s/test/actor_state_http", externalURL)
	grpcURL := fmt.Sprintf("%s/test/actor_state_grpc", externalURL)

	// This initial probe makes the test wait a little bit longer when needed,
	// making this test less flaky due to delays in the deployment.
	_, err := utils.HTTPGetNTimes(externalURL, numHealthChecks)
	require.NoError(t, err)

	// Wait until runtime finds the leader of placements.
	time.Sleep(15 * time.Second)

	t.Run("http", func(t *testing.T) {
		t.Run("getting state which does not exist should error", func(t *testing.T) {
			uuid, err := uuid.NewUUID()
			require.NoError(t, err)
			actuid := uuid.String()

			resp, code, err := utils.HTTPGetWithStatus(fmt.Sprintf("%s/httpMyActorType/%s-myActorID", initActorURL, actuid))
			assert.NoError(t, err)
			assert.Equal(t, http.StatusOK, code)
			assert.Empty(t, resp, "%s", resp)

			resp, code, err = utils.HTTPGetWithStatus(fmt.Sprintf("%s/httpMyActorType/%s-myActorID/doesnotexist", httpURL, actuid))
			assert.NoError(t, err)
			assert.Equal(t, http.StatusNoContent, code)
			assert.Empty(t, resp)
		})

		t.Run("should be able to save, get, update and delete state", func(t *testing.T) {
			uuid, err := uuid.NewUUID()
			require.NoError(t, err)
			actuid := uuid.String()

			_, code, err := utils.HTTPGetWithStatus(fmt.Sprintf("%s/httpMyActorType/%s-myActorID", initActorURL, actuid))
			assert.NoError(t, err)
			assert.Equal(t, http.StatusOK, code)

			myData := []byte(`[{"operation":"upsert","request":{"key":"myKey","value":"myData"}}]`)
			resp, code, err := utils.HTTPPostWithStatus(fmt.Sprintf("%s/httpMyActorType/%s-myActorID", httpURL, actuid), myData)
			assert.NoError(t, err)
			assert.Equal(t, http.StatusNoContent, code)
			assert.Empty(t, string(resp))

			resp, code, err = utils.HTTPGetWithStatus(fmt.Sprintf("%s/httpMyActorType/%s-myActorID/myKey", httpURL, actuid))
			assert.NoError(t, err)
			assert.Equal(t, http.StatusOK, code)
			assert.Equal(t, `"myData"`, string(resp))

			_, code, err = utils.HTTPGetWithStatus(fmt.Sprintf("%s/httpMyActorType/%s-notMyActorID/myKey", httpURL, actuid))
			assert.NoError(t, err)
			assert.Equal(t, http.StatusBadRequest, code)

			newData := []byte(`[{"operation":"upsert","request":{"key":"myKey","value":"newData"}}]`)
			resp, code, err = utils.HTTPPostWithStatus(fmt.Sprintf("%s/httpMyActorType/%s-myActorID", httpURL, actuid), newData)
			assert.NoError(t, err)
			assert.Equal(t, http.StatusNoContent, code)

			resp, code, err = utils.HTTPGetWithStatus(fmt.Sprintf("%s/httpMyActorType/%s-myActorID/myKey", httpURL, actuid))
			assert.NoError(t, err)
			assert.Equal(t, http.StatusOK, code)
			assert.Equal(t, `"newData"`, string(resp))

			deleteData := []byte(`[{"operation":"delete","request":{"key":"myKey"}}]`)
			resp, code, err = utils.HTTPPostWithStatus(fmt.Sprintf("%s/httpMyActorType/%s-myActorID", httpURL, actuid), deleteData)
			assert.NoError(t, err)
			assert.Equal(t, http.StatusNoContent, code)
			assert.Empty(t, string(resp))

			resp, code, err = utils.HTTPGetWithStatus(fmt.Sprintf("%s/httpMyActorType/%s-myActorID/myKey", httpURL, actuid))
			assert.NoError(t, err)
			assert.Equal(t, http.StatusNoContent, code)
			assert.Empty(t, string(resp))
		})

		t.Run("data saved with TTL should be automatically deleted", func(t *testing.T) {
			uuid, err := uuid.NewUUID()
			require.NoError(t, err)
			actuid := uuid.String()

			_, code, err := utils.HTTPGetWithStatus(fmt.Sprintf("%s/httpMyActorType/%s-myActorID", initActorURL, actuid))
			assert.NoError(t, err)
			assert.Equal(t, http.StatusOK, code)

			myData := []byte(`[{"operation":"upsert","request":{"key":"myTTLKey","value":"myTTLData","metadata":{"ttlInSeconds":"5"}}}]`)
			resp, code, err := utils.HTTPPostWithStatus(fmt.Sprintf("%s/httpMyActorType/%s-myActorID", httpURL, actuid), myData)
			assert.NoError(t, err)
			assert.Equal(t, http.StatusNoContent, code)
			assert.Empty(t, string(resp))

			// Ensure the data isn't deleted yet.
			resp, code, err = utils.HTTPGetWithStatus(fmt.Sprintf("%s/httpMyActorType/%s-myActorID/myTTLKey", httpURL, actuid))
			assert.NoError(t, err)
			assert.Equal(t, http.StatusOK, code)
			assert.Equal(t, `"myTTLData"`, string(resp))

			assert.Eventually(t, func() bool {
				resp, code, err = utils.HTTPGetWithStatus(fmt.Sprintf("%s/httpMyActorType/%s-myActorID/myTTLKey", httpURL, actuid))
				return err == nil && code == http.StatusNoContent && string(resp) == ""
			}, 10*time.Second, time.Second/2, "state should be deleted after TTL: %s", code)
		})
	})

	t.Run("grpc", func(t *testing.T) {
		t.Run("getting state which does not exist should error", func(t *testing.T) {
			uuid, err := uuid.NewUUID()
			require.NoError(t, err)
			actuid := uuid.String()

			_, code, err := utils.HTTPGetWithStatus(fmt.Sprintf("%s/grpcMyActorType/%s-myActorID", initActorURL, actuid))
			assert.NoError(t, err)
			assert.Equal(t, http.StatusOK, code)

			b, err := json.Marshal(&runtimev1.GetActorStateRequest{
				ActorType: "grpcMyActorType", ActorId: fmt.Sprintf("%s-myActorID", actuid), Key: "doesnotexist",
			})
			require.NoError(t, err)

			resp, code, err := utils.HTTPGetWithStatusWithData(grpcURL, b)
			assert.NoError(t, err)
			assert.Equal(t, http.StatusOK, code)
			assert.Equal(t, "{}", string(resp))
		})

		t.Run("should be able to save, get, update and delete state", func(t *testing.T) {
			uuid, err := uuid.NewUUID()
			require.NoError(t, err)
			actuid := uuid.String()

			_, code, err := utils.HTTPGetWithStatus(fmt.Sprintf("%s/grpcMyActorType/%s-myActorID", initActorURL, actuid))
			assert.NoError(t, err)
			assert.Equal(t, http.StatusOK, code)

			b, err := json.Marshal(&runtimev1.ExecuteActorStateTransactionRequest{
				ActorType: "grpcMyActorType", ActorId: fmt.Sprintf("%s-myActorID", actuid),
				Operations: []*runtimev1.TransactionalActorStateOperation{
					{OperationType: "upsert", Key: "myKey",
						Value: &anypb.Any{Value: []byte("myData")}},
				},
			})
			require.NoError(t, err)
			_, code, err = utils.HTTPPostWithStatus(grpcURL, b)
			assert.NoError(t, err)
			assert.Equal(t, http.StatusOK, code)

			b, err = json.Marshal(&runtimev1.GetActorStateRequest{
				ActorType: "grpcMyActorType", ActorId: fmt.Sprintf("%s-myActorID", actuid),
				Key: "myKey",
			})
			require.NoError(t, err)
			resp, code, err := utils.HTTPGetWithStatusWithData(grpcURL, b)
			assert.NoError(t, err)
			assert.Equal(t, http.StatusOK, code)
			var gresp runtimev1.GetActorStateResponse
			assert.NoError(t, json.Unmarshal(resp, &gresp))
			assert.Equal(t, []byte("myData"), gresp.Data)

			b, err = json.Marshal(&runtimev1.GetActorStateRequest{
				ActorType: "grpcMyActorType", ActorId: "notmyActorID", Key: "myKey",
			})
			require.NoError(t, err)
			_, code, err = utils.HTTPGetWithStatusWithData(grpcURL, b)
			assert.NoError(t, err)
			assert.Equal(t, http.StatusInternalServerError, code)

			b, err = json.Marshal(&runtimev1.ExecuteActorStateTransactionRequest{
				ActorType: "grpcMyActorType", ActorId: fmt.Sprintf("%s-myActorID", actuid),
				Operations: []*runtimev1.TransactionalActorStateOperation{
					{OperationType: "upsert", Key: "myKey",
						Value: &anypb.Any{Value: []byte("newData")}},
				},
			})
			require.NoError(t, err)
			resp, code, err = utils.HTTPPostWithStatus(grpcURL, b)
			assert.NoError(t, err)
			assert.Equal(t, http.StatusOK, code)
			assert.Empty(t, string(resp))

			b, err = json.Marshal(&runtimev1.GetActorStateRequest{
				ActorType: "grpcMyActorType", ActorId: fmt.Sprintf("%s-myActorID", actuid), Key: "myKey",
			})
			require.NoError(t, err)
			resp, code, err = utils.HTTPGetWithStatusWithData(grpcURL, b)
			assert.NoError(t, err)
			assert.Equal(t, http.StatusOK, code)
			assert.NoError(t, json.Unmarshal(resp, &gresp))
			assert.Equal(t, []byte("newData"), gresp.Data)

			b, err = json.Marshal(&runtimev1.ExecuteActorStateTransactionRequest{
				ActorType: "grpcMyActorType", ActorId: fmt.Sprintf("%s-myActorID", actuid),
				Operations: []*runtimev1.TransactionalActorStateOperation{
					{OperationType: "delete", Key: "myKey"},
				},
			})
			require.NoError(t, err)
			_, code, err = utils.HTTPPostWithStatus(grpcURL, b)
			assert.NoError(t, err)
			assert.Equal(t, http.StatusOK, code)

			b, err = json.Marshal(&runtimev1.GetActorStateRequest{
				ActorType: "grpcMyActorType", ActorId: fmt.Sprintf("%s-myActorID", actuid), Key: "myKey",
			})
			require.NoError(t, err)
			resp, code, err = utils.HTTPGetWithStatusWithData(grpcURL, b)
			assert.NoError(t, err)
			assert.Equal(t, http.StatusOK, code)
			assert.Equal(t, "{}", string(resp))
		})

		t.Run("data saved with TTL should be automatically deleted", func(t *testing.T) {
			uuid, err := uuid.NewUUID()
			require.NoError(t, err)
			actuid := uuid.String()

			_, code, err := utils.HTTPGetWithStatus(fmt.Sprintf("%s/grpcMyActorType/%s-myActorIDTTL", initActorURL, actuid))
			assert.NoError(t, err)
			assert.Equal(t, http.StatusOK, code)

			b, err := json.Marshal(&runtimev1.ExecuteActorStateTransactionRequest{
				ActorType: "grpcMyActorType", ActorId: fmt.Sprintf("%s-myActorIDTTL", actuid),
				Operations: []*runtimev1.TransactionalActorStateOperation{
					{
						OperationType: "upsert", Key: "myTTLKey",
						Value:    &anypb.Any{Value: []byte("myData")},
						Metadata: map[string]string{"ttlInSeconds": "3"},
					},
				},
			})
			require.NoError(t, err)
			_, code, err = utils.HTTPPostWithStatus(grpcURL, b)
			assert.NoError(t, err)
			assert.Equal(t, http.StatusOK, code)

			assert.Eventually(t, func() bool {
				b, err = json.Marshal(&runtimev1.GetActorStateRequest{
					ActorType: "grpcMyActorType", ActorId: fmt.Sprintf("%s-myActorIDTTL", actuid),
					Key: "myTTLKey",
				})
				require.NoError(t, err)
				resp, code, err := utils.HTTPGetWithStatusWithData(grpcURL, b)
				return err == nil && code == http.StatusOK && string(resp) == "{}"
			}, 10*time.Second, time.Second/2)
		})
	})
}
