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

package resultreminder

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	procscheduler "github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(orphan))
}

// orphan pins the fate of an activity-result reminder whose target workflow
// instance no longer exists: it must be acknowledged and deleted, not
// retried forever. The activity's host cannot reach the workflow app (all
// its daprds are gone), so the result is durably queued as an
// activity-result reminder; the workflow app then returns with an empty
// state store, i.e. the instance is gone the way a purge leaves it. Before
// the fix the reminder's retry-forever failure policy refired it every
// second indefinitely, permanently loading every host of the workflow actor
// type.
type orphan struct {
	place     *placement.Placement
	scheduler *procscheduler.Scheduler
}

func (o *orphan) Setup(t *testing.T) []framework.Option {
	o.place = placement.New(t)
	o.scheduler = procscheduler.New(t)

	return []framework.Option{
		framework.WithProcesses(o.place, o.scheduler),
	}
}

func (o *orphan) Run(t *testing.T, ctx context.Context) {
	o.scheduler.WaitUntilRunning(t, ctx)
	o.place.WaitUntilRunning(t, ctx)

	appA := uuid.New().String()
	appB := uuid.New().String()

	newDaprd := func(appID string) *daprd.Daprd {
		return daprd.New(t,
			daprd.WithAppID(appID),
			daprd.WithInMemoryActorStateStore("statestore"),
			daprd.WithPlacementAddresses(o.place.Address()),
			daprd.WithSchedulerAddresses(o.scheduler.Address()),
		)
	}

	daprdA := newDaprd(appA)
	daprdB := newDaprd(appB)
	daprdA.Run(t, ctx)
	daprdB.Run(t, ctx)
	t.Cleanup(func() { daprdB.Cleanup(t) })
	daprdA.WaitUntilRunning(t, ctx)
	daprdB.WaitUntilRunning(t, ctx)

	regA := task.NewTaskRegistry()
	require.NoError(t, regA.AddWorkflowN("Orphaned", func(c *task.WorkflowContext) (any, error) {
		var out string
		err := c.CallActivity("Slow",
			task.WithActivityInput("x"),
			task.WithActivityAppID(appB),
		).Await(&out)
		return out, err
	}))

	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	started := make(chan struct{}, 1)
	regB := task.NewTaskRegistry()
	require.NoError(t, regB.AddActivityN("Slow", func(c task.ActivityContext) (any, error) {
		select {
		case started <- struct{}{}:
		default:
		}
		<-block
		return "done", nil
	}))

	clientA := client.NewTaskHubGrpcClient(daprdA.GRPCConn(t, ctx), backend.DefaultLogger())
	require.NoError(t, clientA.StartWorkItemListener(ctx, regA))
	clientB := client.NewTaskHubGrpcClient(daprdB.GRPCConn(t, ctx), backend.DefaultLogger())
	require.NoError(t, clientB.StartWorkItemListener(ctx, regB))

	_, err := daprdA.GRPCClient(t, ctx).StartWorkflowBeta1(ctx, &rtv1.StartWorkflowRequest{
		WorkflowComponent: "dapr",
		WorkflowName:      "Orphaned",
	})
	require.NoError(t, err)

	select {
	case <-started:
	case <-time.After(time.Second * 20):
		require.Fail(t, "timed out waiting for the activity to start")
	}

	// Take the whole workflow app away, then let the activity finish: its
	// result cannot be delivered (no host serves the workflow actor type),
	// so the activity actor durably queues it as an activity-result
	// reminder.
	daprdA.Cleanup(t)
	block <- struct{}{}

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, activityResultJobCount(t, ctx, o.scheduler), 1,
			"the undeliverable result must be queued as an activity-result reminder")
	}, time.Second*30, time.Millisecond*50)

	// The workflow app returns with an EMPTY state store: the instance is
	// gone, exactly as a purge leaves it. The queued reminder now targets a
	// nonexistent instance.
	daprdA2 := newDaprd(appA)
	daprdA2.Run(t, ctx)
	t.Cleanup(func() { daprdA2.Cleanup(t) })
	daprdA2.WaitUntilRunning(t, ctx)

	// The workflow actor types are only hosted once an app connects; the
	// registry does not resurrect the missing instance.
	clientA2 := client.NewTaskHubGrpcClient(daprdA2.GRPCConn(t, ctx), backend.DefaultLogger())
	require.NoError(t, clientA2.StartWorkItemListener(ctx, regA))

	// The fire against the missing instance must be ACKED so the scheduler
	// deletes the one-shot. Before the fix it returned an unclassified
	// instance-not-found error and the retry-forever failure policy refired
	// it every second, so the job never left the scheduler.
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Zero(c, activityResultJobCount(t, ctx, o.scheduler),
			"an activity-result reminder for a missing instance must be acked and deleted, not retried")
	}, time.Second*30, time.Millisecond*50)
}

// activityResultJobCount counts scheduler jobs for activity-result reminders.
func activityResultJobCount(t *testing.T, ctx context.Context, s *procscheduler.Scheduler) int {
	t.Helper()
	var count int
	for _, key := range s.ListAllKeys(t, ctx, "dapr/jobs") {
		if strings.Contains(key, "activity-result") {
			count++
		}
	}
	return count
}
