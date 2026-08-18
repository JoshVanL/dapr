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

package workflow

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	wferrors "github.com/dapr/dapr/pkg/runtime/wfengine/state/errors"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	dworkflow "github.com/dapr/durabletask-go/workflow"
)

// ParkedRegistry returns a registry with a single workflow of the given name
// that parks on the "continue" external event. The long timeout keeps timer
// turns from interfering with tamper detection assertions.
func ParkedRegistry(name string) *dworkflow.Registry {
	reg := dworkflow.NewRegistry()
	reg.AddWorkflowN(name, func(ctx *dworkflow.WorkflowContext) (any, error) {
		var payload string
		if err := ctx.WaitForExternalEvent("continue", time.Minute*5).Await(&payload); err != nil {
			return nil, err
		}
		return payload, nil
	})
	return reg
}

// ActivityParkedRegistry is ParkedRegistry with a preceding "noop" activity
// call, for tests that need at least two signature batches in history before
// the workflow parks.
func ActivityParkedRegistry(name string) *dworkflow.Registry {
	reg := dworkflow.NewRegistry()
	reg.AddWorkflowN(name, func(ctx *dworkflow.WorkflowContext) (any, error) {
		if err := ctx.CallActivity("noop").Await(nil); err != nil {
			return nil, err
		}
		var payload string
		if err := ctx.WaitForExternalEvent("continue", time.Minute*5).Await(&payload); err != nil {
			return nil, err
		}
		return payload, nil
	})
	reg.AddActivityN("noop", func(ctx dworkflow.ActivityContext) (any, error) {
		return nil, nil
	})
	return reg
}

// StartParkedWorkflow starts a worker for the given registry, schedules the
// named workflow, and waits until it is running.
func StartParkedWorkflow(t *testing.T, ctx context.Context, client *dworkflow.Client, reg *dworkflow.Registry, name string) string {
	t.Helper()

	require.NoError(t, client.StartWorker(ctx, reg))

	id, err := client.ScheduleWorkflow(ctx, name)
	require.NoError(t, err)

	meta, err := client.WaitForWorkflowStart(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, dworkflow.StatusRunning, meta.RuntimeStatus)

	return id
}

// WaitForTampered asserts the workflow reaches the terminal FAILED state with
// the well-known history-tampered error type within the given window.
func WaitForTampered(t *testing.T, ctx context.Context, client *dworkflow.Client, id string, within time.Duration) {
	t.Helper()

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		wmeta, werr := client.FetchWorkflowMetadata(ctx, id)
		assert.NoError(c, werr)
		if !assert.NotNil(c, wmeta) {
			return
		}
		if !assert.Equal(c, dworkflow.StatusFailed, wmeta.RuntimeStatus) {
			return
		}
		if !assert.NotNil(c, wmeta.FailureDetails) {
			return
		}
		assert.Equal(c, wferrors.ErrorTypeHistoryTampered, wmeta.FailureDetails.GetErrorType())
	}, within, time.Millisecond*10)
}

// IntegrityAuditCount sums the background integrity audit count metric across
// tags for the given audit result.
func IntegrityAuditCount(t assert.TestingT, ctx context.Context, d *daprd.Daprd, result string) float64 {
	var total float64
	for k, v := range d.Metrics(t, ctx).All() {
		if strings.Contains(k, "integrity_audit_count") && strings.Contains(k, "audit_result:"+result) {
			total += v
		}
	}
	return total
}
