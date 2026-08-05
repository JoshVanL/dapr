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

package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	procscheduler "github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(activityv2restart))
}

// activityv2restart is the activity-v2 no-stranding proof: an activity is
// dispatched with its run-activity reminder elided, the daprd hosting the
// in-flight execution is killed (nothing durable exists on the activity
// side), and a fresh daprd must complete the workflow via the orchestrator
// janitor's re-dispatch of the unresolved TaskScheduled event. The janitor
// period is shortened via the test env override so recovery is observable
// quickly.
type activityv2restart struct {
	place     *placement.Placement
	scheduler *procscheduler.Scheduler
	db        *sqlite.SQLite
}

func (a *activityv2restart) Setup(t *testing.T) []framework.Option {
	a.place = placement.New(t)
	a.scheduler = procscheduler.New(t)
	a.db = sqlite.New(t,
		sqlite.WithActorStateStore(true),
		sqlite.WithMetadata("busyTimeout", "10s"),
		sqlite.WithMetadata("disableWAL", "true"),
	)

	return []framework.Option{
		framework.WithProcesses(a.place, a.scheduler, a.db),
	}
}

func (a *activityv2restart) Run(t *testing.T, ctx context.Context) {
	a.scheduler.WaitUntilRunning(t, ctx)
	a.place.WaitUntilRunning(t, ctx)

	appID := uuid.New().String()
	newDaprd := func() *daprd.Daprd {
		return daprd.New(t,
			daprd.WithAppID(appID),
			daprd.WithResourceFiles(a.db.GetComponent(t)),
			daprd.WithPlacementAddresses(a.place.Address()),
			daprd.WithSchedulerAddresses(a.scheduler.Address()),
			daprd.WithConfigManifests(t, localActivityFeatureConfig),
			daprd.WithExecOptions(exec.WithEnvVars(t, "DAPR_WORKFLOW_JANITOR_PERIOD", "2s")),
		)
	}

	newRegistry := func(activity func(task.ActivityContext) (any, error)) *task.TaskRegistry {
		r := task.NewTaskRegistry()
		require.NoError(t, r.AddWorkflowN("OneActivity", func(c *task.WorkflowContext) (any, error) {
			var out string
			if err := c.CallActivity("SayHello", task.WithActivityInput("Dapr")).Await(&out); err != nil {
				return nil, err
			}
			return out, nil
		}))
		require.NoError(t, r.AddActivityN("SayHello", activity))
		return r
	}

	// Phase 1: start the workflow on the first daprd with an activity that
	// blocks forever, so the execution is in flight with no durable
	// artifact of its own (the run-activity reminder is elided).
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	started := make(chan struct{}, 1)
	registry1 := newRegistry(func(task.ActivityContext) (any, error) {
		select {
		case started <- struct{}{}:
		default:
		}
		<-block
		return nil, nil
	})

	daprd1 := newDaprd()
	daprd1.Run(t, ctx)
	daprd1.WaitUntilRunning(t, ctx)

	client1 := client.NewTaskHubGrpcClient(daprd1.GRPCConn(t, ctx), backend.DefaultLogger())
	require.NoError(t, client1.StartWorkItemListener(ctx, registry1))

	resp, err := daprd1.GRPCClient(t, ctx).StartWorkflowBeta1(ctx, &rtv1.StartWorkflowRequest{
		WorkflowComponent: "dapr",
		WorkflowName:      "OneActivity",
	})
	require.NoError(t, err)

	select {
	case <-started:
	case <-time.After(time.Second * 20):
		require.Fail(t, "timed out waiting for the activity to start executing")
	}

	// Wait for the dispatching turn to durably commit (RUNNING status +
	// janitor job present) so the kill lands squarely in the "TaskScheduled
	// committed, execution in flight, nothing durable on the activity side"
	// window that only the janitor re-dispatch covers.
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		meta, merr := client1.FetchWorkflowMetadata(ctx, api.InstanceID(resp.GetInstanceId()))
		if assert.NoError(c, merr) {
			assert.Equal(c, "ORCHESTRATION_STATUS_RUNNING", meta.GetRuntimeStatus().String())
		}
		janitors, _ := jobCounts(t, ctx, a.scheduler)
		assert.Equal(c, 1, janitors, "the janitor must be armed before the activity dispatch was acked")
	}, time.Second*20, time.Millisecond*50)
	assert.Zero(t, runActivityJobCount(t, ctx, a.scheduler),
		"the in-flight activity must have no durable run-activity job")

	// Phase 2: kill the daprd mid-execution.
	daprd1.Cleanup(t)

	// Phase 3: a fresh daprd (same app, same state store, working activity
	// handler) must complete the workflow with no new stimulus, via the
	// janitor re-dispatch.
	registry2 := newRegistry(func(c task.ActivityContext) (any, error) {
		return "recovered", nil
	})

	daprd2 := newDaprd()
	daprd2.Run(t, ctx)
	t.Cleanup(func() { daprd2.Cleanup(t) })
	daprd2.WaitUntilRunning(t, ctx)

	client2 := client.NewTaskHubGrpcClient(daprd2.GRPCConn(t, ctx), backend.DefaultLogger())
	require.NoError(t, client2.StartWorkItemListener(ctx, registry2))

	wctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	metadata, err := client2.WaitForWorkflowCompletion(wctx, api.InstanceID(resp.GetInstanceId()))
	require.NoError(t, err)
	assert.True(t, api.WorkflowMetadataIsComplete(metadata))
	assert.Equal(t, `"recovered"`, metadata.GetOutput().GetValue())

	// The recovery was the janitor's re-dispatch.
	assert.GreaterOrEqual(t, localActivityStatusCount(t, ctx, daprd2, "janitor_redispatched"), float64(1),
		"the unresolved activity must have been re-dispatched by the janitor")

	// Terminal cleanup holds across the restart too.
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		janitors, newEvents := jobCounts(t, ctx, a.scheduler)
		assert.Zero(c, janitors)
		assert.Zero(c, newEvents)
		assert.Zero(c, runActivityJobCount(t, ctx, a.scheduler))
	}, time.Second*60, time.Millisecond*50)
}
