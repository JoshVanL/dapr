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
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	procscheduler "github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(activityv2))
}

const localActivityFeatureConfig = `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: localactivityfastpath
spec:
  features:
  - name: WorkflowsLocalWakeFastPath
    enabled: true
  - name: WorkflowsLocalActivityFastPath
    enabled: true
`

// activityv2 verifies the job accounting of WorkflowsLocalActivityFastPath:
// during a healthy run NO run-activity one-shot jobs are created (the
// upsert+delete commit pair elision), the wake-v2 accounting still holds
// (one janitor, no per-event new-event jobs), and everything is cleaned up
// at the terminal turn.
type activityv2 struct {
	daprd     *daprd.Daprd
	place     *placement.Placement
	scheduler *procscheduler.Scheduler
}

func (a *activityv2) Setup(t *testing.T) []framework.Option {
	app := app.New(t)
	a.place = placement.New(t)
	a.scheduler = procscheduler.New(t)
	a.daprd = daprd.New(t,
		daprd.WithAppPort(app.Port()),
		daprd.WithPlacementAddresses(a.place.Address()),
		daprd.WithInMemoryActorStateStore("statestore"),
		daprd.WithSchedulerAddresses(a.scheduler.Address()),
		daprd.WithConfigManifests(t, localActivityFeatureConfig),
	)

	return []framework.Option{
		framework.WithProcesses(a.scheduler, a.place, app, a.daprd),
	}
}

func (a *activityv2) Run(t *testing.T, ctx context.Context) {
	a.scheduler.WaitUntilRunning(t, ctx)
	a.place.WaitUntilRunning(t, ctx)
	a.daprd.WaitUntilRunning(t, ctx)

	r := task.NewTaskRegistry()
	require.NoError(t, r.AddWorkflowN("ActivityEventActivity", func(c *task.WorkflowContext) (any, error) {
		var mid string
		if err := c.CallActivity("SayHello", task.WithActivityInput("Dapr")).Await(&mid); err != nil {
			return nil, err
		}
		if err := c.WaitForSingleEvent("go", time.Minute).Await(new([]byte)); err != nil {
			return nil, err
		}
		var out string
		if err := c.CallActivity("SayHello", task.WithActivityInput(mid)).Await(&out); err != nil {
			return nil, err
		}
		return out, nil
	}))
	require.NoError(t, r.AddActivityN("SayHello", func(c task.ActivityContext) (any, error) {
		var inp string
		if err := c.GetInput(&inp); err != nil {
			return nil, err
		}
		return fmt.Sprintf("Hello, %s!", inp), nil
	}))

	backendClient := client.NewTaskHubGrpcClient(a.daprd.GRPCConn(t, ctx), backend.DefaultLogger())
	require.NoError(t, backendClient.StartWorkItemListener(ctx, r))

	resp, err := a.daprd.GRPCClient(t, ctx).StartWorkflowBeta1(ctx, &rtv1.StartWorkflowRequest{
		WorkflowComponent: "dapr",
		WorkflowName:      "ActivityEventActivity",
	})
	require.NoError(t, err)

	// Parked on the external event with the first activity completed: the
	// activity ran locally without ever creating its run-activity job, and
	// the wake-v2 accounting holds.
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		janitors, newEvents := jobCounts(t, ctx, a.scheduler)
		assert.Equal(c, 1, janitors, "exactly one janitor backstop while the instance runs")
		assert.Zero(c, newEvents, "wake v2 must not create per-event new-event one-shot jobs")
		assert.GreaterOrEqual(c, localActivityStatusCount(t, ctx, a.daprd, "success"), float64(1),
			"the first activity must have been driven locally")
	}, time.Second*20, time.Millisecond*50)
	assert.Zero(t, runActivityJobCount(t, ctx, a.scheduler),
		"activity v2 must not create run-activity one-shot jobs")

	_, err = a.daprd.GRPCClient(t, ctx).RaiseEventWorkflowBeta1(ctx, &rtv1.RaiseEventWorkflowRequest{
		InstanceId:        resp.GetInstanceId(),
		WorkflowComponent: "dapr",
		EventName:         "go",
	})
	require.NoError(t, err)

	metadata, err := backendClient.WaitForWorkflowCompletion(ctx, api.InstanceID(resp.GetInstanceId()))
	require.NoError(t, err)
	assert.True(t, api.WorkflowMetadataIsComplete(metadata))
	assert.Equal(t, `"Hello, Hello, Dapr!!"`, metadata.GetOutput().GetValue())

	// Terminal turn: janitor deleted, no new-event or run-activity residue.
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		janitors, newEvents := jobCounts(t, ctx, a.scheduler)
		assert.Zero(c, janitors, "the janitor must be deleted at the terminal turn")
		assert.Zero(c, newEvents)
		assert.Zero(c, runActivityJobCount(t, ctx, a.scheduler))
	}, time.Second*60, time.Millisecond*50)

	// Both activities drove locally; no janitor rescue was needed.
	assert.GreaterOrEqual(t, localActivityStatusCount(t, ctx, a.daprd, "success"), float64(2))
	assert.Zero(t, localActivityStatusCount(t, ctx, a.daprd, "janitor_redispatched"),
		"a healthy run must not need janitor re-dispatch")
}

// runActivityJobCount returns the number of run-activity one-shot jobs
// currently in the scheduler.
func runActivityJobCount(t *testing.T, ctx context.Context, s *procscheduler.Scheduler) int {
	t.Helper()
	var count int
	for _, key := range s.ListAllKeys(t, ctx, "dapr/jobs") {
		if strings.Contains(key, "run-activity") {
			count++
		}
	}
	return count
}

// localActivityStatusCount sums the local_activity metric series matching the
// given status.
func localActivityStatusCount(t *testing.T, ctx context.Context, d *daprd.Daprd, status string) float64 {
	t.Helper()
	var count float64
	for k, v := range d.Metrics(t, ctx).All() {
		if strings.Contains(k, "local_activity") && strings.Contains(k, status) {
			count += v
		}
	}
	return count
}
