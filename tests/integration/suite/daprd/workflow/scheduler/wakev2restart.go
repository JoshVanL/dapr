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
	suite.Register(new(wakev2restart))
}

// wakev2restart is the wake-v2 no-stranding proof: an external event is
// raised (durably saved to the inbox, acked to the client) and the daprd is
// then stopped before/while the local drive runs. No per-event new-event
// reminder exists under wake v2, so the replacement daprd must recover the
// pending inbox via the durable backstops (the janitor reminder, or the
// escalated durable reminder when the shutdown raced the drive) and run the
// workflow to completion. The janitor period is shortened via the test env
// override so recovery is observable quickly.
type wakev2restart struct {
	place     *placement.Placement
	scheduler *procscheduler.Scheduler
	db        *sqlite.SQLite
}

func (w *wakev2restart) Setup(t *testing.T) []framework.Option {
	w.place = placement.New(t)
	w.scheduler = procscheduler.New(t)
	w.db = sqlite.New(t,
		sqlite.WithActorStateStore(true),
		sqlite.WithMetadata("busyTimeout", "10s"),
		sqlite.WithMetadata("disableWAL", "true"),
	)

	return []framework.Option{
		framework.WithProcesses(w.place, w.scheduler, w.db),
	}
}

func (w *wakev2restart) Run(t *testing.T, ctx context.Context) {
	w.scheduler.WaitUntilRunning(t, ctx)
	w.place.WaitUntilRunning(t, ctx)

	appID := uuid.New().String()
	newDaprd := func() *daprd.Daprd {
		return daprd.New(t,
			daprd.WithAppID(appID),
			daprd.WithResourceFiles(w.db.GetComponent(t)),
			daprd.WithPlacementAddresses(w.place.Address()),
			daprd.WithSchedulerAddresses(w.scheduler.Address()),
			daprd.WithConfigManifests(t, localWakeFeatureConfig),
			daprd.WithExecOptions(exec.WithEnvVars(t, "DAPR_WORKFLOW_JANITOR_PERIOD", "2s")),
		)
	}

	registry := task.NewTaskRegistry()
	require.NoError(t, registry.AddWorkflowN("WaitForGo", func(c *task.WorkflowContext) (any, error) {
		if err := c.WaitForSingleEvent("go", time.Minute*3).Await(new([]byte)); err != nil {
			return nil, err
		}
		return "done", nil
	}))

	// Phase 1: start the workflow on the first daprd and park it on the
	// external event.
	daprd1 := newDaprd()
	daprd1.Run(t, ctx)
	daprd1.WaitUntilRunning(t, ctx)

	client1 := client.NewTaskHubGrpcClient(daprd1.GRPCConn(t, ctx), backend.DefaultLogger())
	require.NoError(t, client1.StartWorkItemListener(ctx, registry))

	resp, err := daprd1.GRPCClient(t, ctx).StartWorkflowBeta1(ctx, &rtv1.StartWorkflowRequest{
		WorkflowComponent: "dapr",
		WorkflowName:      "WaitForGo",
	})
	require.NoError(t, err)

	// Wait until the instance is parked on the external event. No janitor
	// exists yet: the start path keeps its own durable reminder, and the
	// janitor is asserted by the first driveNewEvent (the raise below),
	// BEFORE the raise is acked, which is exactly the backstop ordering this
	// test exercises.
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		meta, merr := client1.FetchWorkflowMetadata(ctx, api.InstanceID(resp.GetInstanceId()))
		if assert.NoError(c, merr) {
			assert.Equal(c, "ORCHESTRATION_STATUS_RUNNING", meta.GetRuntimeStatus().String())
		}
	}, time.Second*20, time.Millisecond*50)

	// Phase 2: raise the event (inbox durably saved, ack returned) and stop
	// the daprd immediately after. Whether the local drive won the race or
	// not, no per-event reminder protects the row: recovery is on the
	// janitor (or the escalated reminder if the drive observed the
	// shutdown).
	_, err = daprd1.GRPCClient(t, ctx).RaiseEventWorkflowBeta1(ctx, &rtv1.RaiseEventWorkflowRequest{
		InstanceId:        resp.GetInstanceId(),
		WorkflowComponent: "dapr",
		EventName:         "go",
	})
	require.NoError(t, err)
	daprd1.Cleanup(t)

	// Phase 3: a fresh daprd (same app, same state store) must complete the
	// workflow without any new stimulus.
	daprd2 := newDaprd()
	daprd2.Run(t, ctx)
	t.Cleanup(func() { daprd2.Cleanup(t) })
	daprd2.WaitUntilRunning(t, ctx)

	client2 := client.NewTaskHubGrpcClient(daprd2.GRPCConn(t, ctx), backend.DefaultLogger())
	require.NoError(t, client2.StartWorkItemListener(ctx, registry))

	wctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	metadata, err := client2.WaitForWorkflowCompletion(wctx, api.InstanceID(resp.GetInstanceId()))
	require.NoError(t, err)
	assert.True(t, api.WorkflowMetadataIsComplete(metadata))

	// Terminal cleanup holds across the restart too.
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		janitors, newEvents := jobCounts(t, ctx, w.scheduler)
		assert.Zero(c, janitors)
		assert.Zero(c, newEvents)
	}, time.Second*60, time.Millisecond*50)
}
