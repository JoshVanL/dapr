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

package config

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
	suite.Register(new(retentionstuck))
}

type retentionstuck struct {
	workflow *workflow.Workflow
}

func (r *retentionstuck) Setup(t *testing.T) []framework.Option {
	r.workflow = workflow.New(t,
		workflow.WithDaprdOptions(0, daprd.WithConfigManifests(t, `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: wfpolicy
spec:
  workflow:
    stateRetentionPolicy:
      anyTerminal: "3s"
`)),
	)

	return []framework.Option{
		framework.WithProcesses(r.workflow),
	}
}

func (r *retentionstuck) Run(t *testing.T, ctx context.Context) {
	r.workflow.WaitUntilRunning(t, ctx)

	// Run 1 completes immediately. Run 2 blocks on an unraised event so its
	// runtime status stays non-completed past the retention dueTime.
	r.workflow.Registry().AddOrchestratorN("foo", func(ctx *task.OrchestrationContext) (any, error) {
		_ = ctx.WaitForSingleEvent("Continue", -1).Await(nil)
		return nil, nil
	})

	client := r.workflow.BackendClient(t, ctx)

	const instanceID api.InstanceID = "retentionstuck-claim-eval"
	appID := r.workflow.Dapr().AppID()
	retentionPrefix := fmt.Sprintf(
		"dapr/jobs/actorreminder||default||dapr.internal.default.%s.retentioner||%s||",
		appID, instanceID,
	)
	failedPurgeMetric := fmt.Sprintf(
		"dapr_runtime_workflow_operation_count|app_id:%s|namespace:|operation:purge_workflow|status:failed",
		appID,
	)

	reusePolicy := &api.OrchestrationIdReusePolicy{
		Action: api.REUSE_ID_ACTION_TERMINATE,
		OperationStatus: []api.OrchestrationStatus{
			api.RUNTIME_STATUS_RUNNING,
			api.RUNTIME_STATUS_COMPLETED,
			api.RUNTIME_STATUS_PENDING,
		},
	}

	// Run 1: complete immediately. Queues anyterminal reminder at completedAt +
	// 3s.
	id, err := client.ScheduleNewOrchestration(ctx, "foo",
		api.WithInstanceID(instanceID),
		api.WithOrchestrationIdReusePolicy(reusePolicy),
	)
	require.NoError(t, err)
	require.NoError(t, client.RaiseEvent(ctx, id, "Continue"))
	_, err = client.WaitForOrchestrationCompletion(ctx, id)
	require.NoError(t, err)
	require.Len(t, r.workflow.Scheduler().ListAllKeys(t, ctx, retentionPrefix), 1)

	// Run 2: same ID, TERMINATE policy, no event raised. state.Reset() puts the
	// workflow into a non-completed state past the retention dueTime. The
	// schedule can transiently fail with an etag mismatch against the prior
	// run's writes; that race is independent of the retentioner bug we're
	// testing for and the customer's Python handler would just NACK + Kafka
	// redeliver, so retry until the schedule lands.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		_, scherr := client.ScheduleNewOrchestration(ctx, "foo",
			api.WithInstanceID(instanceID),
			api.WithOrchestrationIdReusePolicy(reusePolicy),
		)
		assert.NoError(c, scherr)
	}, 10*time.Second, 250*time.Millisecond)

	// Wait past the retention dueTime so the reminder has fired at least
	// once. With the bug, by now the retentioner has logged at least one
	// StatusFailed purge and is in a 1Hz retry loop.
	time.Sleep(4 * time.Second)

	// Desired contract: the retentioner does not retry forever against a
	// superseded run. Sample the failed-purge counter twice with a 3s
	// window in between. With the bug, the counter climbs by ~3. With the
	// fix (treat ErrNotCompleted like ErrInstanceNotFound, drain
	// silently), the counter is stable.
	before := int(r.workflow.Dapr().Metrics(t, ctx).All()[failedPurgeMetric])
	time.Sleep(3 * time.Second)
	after := int(r.workflow.Dapr().Metrics(t, ctx).All()[failedPurgeMetric])
	assert.Equal(t, before, after,
		"purge_workflow|failed counter climbed from %d to %d during a 3s sample - retentioner is in a 1Hz retry storm against a non-completed run",
		before, after)

	// Cleanup: unblock run 2 so the test exits without leaking a workflow
	// waiting on an unraised event.
	require.NoError(t, client.RaiseEvent(ctx, instanceID, "Continue"))
	_, err = client.WaitForOrchestrationCompletion(ctx, instanceID)
	require.NoError(t, err)
}
