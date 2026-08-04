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
	suite.Register(new(wakev2))
}

// wakev2 verifies the wake-v2 job accounting of WorkflowsLocalWakeFastPath:
// during a healthy run NO per-event new-event one-shot jobs are created (the
// pair elision that removes their upsert+delete commits), exactly one
// repeating janitor backstop exists while the instance runs, and everything
// is cleaned up at the terminal turn.
type wakev2 struct {
	daprd     *daprd.Daprd
	place     *placement.Placement
	scheduler *procscheduler.Scheduler
}

func (w *wakev2) Setup(t *testing.T) []framework.Option {
	app := app.New(t)
	w.place = placement.New(t)
	w.scheduler = procscheduler.New(t)
	w.daprd = daprd.New(t,
		daprd.WithAppPort(app.Port()),
		daprd.WithPlacementAddresses(w.place.Address()),
		daprd.WithInMemoryActorStateStore("statestore"),
		daprd.WithSchedulerAddresses(w.scheduler.Address()),
		daprd.WithConfigManifests(t, localWakeFeatureConfig),
	)

	return []framework.Option{
		framework.WithProcesses(w.scheduler, w.place, app, w.daprd),
	}
}

func (w *wakev2) Run(t *testing.T, ctx context.Context) {
	w.scheduler.WaitUntilRunning(t, ctx)
	w.place.WaitUntilRunning(t, ctx)
	w.daprd.WaitUntilRunning(t, ctx)

	r := task.NewTaskRegistry()
	require.NoError(t, r.AddWorkflowN("ActivityThenEvent", func(c *task.WorkflowContext) (any, error) {
		var out string
		if err := c.CallActivity("SayHello", task.WithActivityInput("Dapr")).Await(&out); err != nil {
			return nil, err
		}
		if err := c.WaitForSingleEvent("go", time.Minute).Await(new([]byte)); err != nil {
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

	backendClient := client.NewTaskHubGrpcClient(w.daprd.GRPCConn(t, ctx), backend.DefaultLogger())
	require.NoError(t, backendClient.StartWorkItemListener(ctx, r))

	resp, err := w.daprd.GRPCClient(t, ctx).StartWorkflowBeta1(ctx, &rtv1.StartWorkflowRequest{
		WorkflowComponent: "dapr",
		WorkflowName:      "ActivityThenEvent",
		Input:             []byte(`"Dapr"`),
	})
	require.NoError(t, err)

	// The workflow is now parked on the external event with the activity
	// completed: the janitor must exist and no per-event new-event one-shot
	// may ever have been created.
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		janitors, newEvents := jobCounts(t, ctx, w.scheduler)
		assert.Equal(c, 1, janitors, "exactly one janitor backstop while the instance runs")
		assert.Zero(c, newEvents, "wake v2 must not create per-event new-event one-shot jobs")
	}, time.Second*20, time.Millisecond*50)

	_, err = w.daprd.GRPCClient(t, ctx).RaiseEventWorkflowBeta1(ctx, &rtv1.RaiseEventWorkflowRequest{
		InstanceId:        resp.GetInstanceId(),
		WorkflowComponent: "dapr",
		EventName:         "go",
	})
	require.NoError(t, err)

	metadata, err := backendClient.WaitForWorkflowCompletion(ctx, api.InstanceID(resp.GetInstanceId()))
	require.NoError(t, err)
	assert.True(t, api.WorkflowMetadataIsComplete(metadata))

	// Terminal turn: the janitor is deleted and no new-event residue remains
	// (the start one-shot self-cleans via its empty-inbox fire).
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		janitors, newEvents := jobCounts(t, ctx, w.scheduler)
		assert.Zero(c, janitors, "the janitor must be deleted at the terminal turn")
		assert.Zero(c, newEvents)
	}, time.Second*60, time.Millisecond*50)

	// All wakes (start, activity completion, raised event) drove locally.
	assert.GreaterOrEqual(t, localWakeSuccessCount(t, ctx, w.daprd), float64(3))
}

// jobCounts returns the number of janitor jobs and per-event new-event
// one-shot jobs currently in the scheduler.
func jobCounts(t *testing.T, ctx context.Context, s *procscheduler.Scheduler) (janitors, newEvents int) {
	t.Helper()
	for _, key := range s.ListAllKeys(t, ctx, "dapr/jobs") {
		if !strings.Contains(key, "new-event") {
			continue
		}
		if strings.Contains(key, "new-event-janitor") {
			janitors++
		} else {
			newEvents++
		}
	}
	return janitors, newEvents
}
