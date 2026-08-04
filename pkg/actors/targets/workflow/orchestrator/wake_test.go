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

package orchestrator

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	actorapi "github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/fake"
	actorreminders "github.com/dapr/dapr/pkg/actors/reminders"
	remindersfake "github.com/dapr/dapr/pkg/actors/reminders/fake"
	"github.com/dapr/dapr/pkg/actors/router"
	routerfake "github.com/dapr/dapr/pkg/actors/router/fake"
	actorstate "github.com/dapr/dapr/pkg/actors/state"
	statefake "github.com/dapr/dapr/pkg/actors/state/fake"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
	wfenginestate "github.com/dapr/dapr/pkg/runtime/wfengine/state"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/backend/runtimestate"
)

// wakeHarness captures state saves, reminder creates/deletes and router
// CallReminder invocations in one ordered log. The wake runs on a detached
// goroutine, so assertions on wake effects must be eventual.
type wakeHarness struct {
	lock sync.Mutex
	ops  []string

	callReminderErr error
	deleteErr       error
	reminderGate    chan struct{} // when non-nil, CallReminder blocks on it (or ctx)

	calls []*actorapi.Reminder

	fact *factory
	orch *orchestrator
}

func (h *wakeHarness) snapshotOps() []string {
	h.lock.Lock()
	defer h.lock.Unlock()
	return append([]string(nil), h.ops...)
}

func newWakeHarness(t *testing.T, instanceID string, fastPath bool) *wakeHarness {
	t.Helper()

	h := new(wakeHarness)

	fakeRems := remindersfake.New().
		WithCreate(func(_ context.Context, req *actorapi.CreateReminderRequest) error {
			h.lock.Lock()
			defer h.lock.Unlock()
			h.ops = append(h.ops, "create:"+req.Name)
			return nil
		}).
		WithDelete(func(_ context.Context, req *actorapi.DeleteReminderRequest) error {
			h.lock.Lock()
			defer h.lock.Unlock()
			if h.deleteErr != nil {
				return h.deleteErr
			}
			h.ops = append(h.ops, "delete:"+req.Name)
			return nil
		})

	fakeState := statefake.New().
		WithGetFn(func(context.Context, *actorapi.GetStateRequest, bool) (*actorapi.StateResponse, error) {
			return &actorapi.StateResponse{}, nil
		}).
		WithTransactionalStateOperationFn(func(context.Context, bool, *actorapi.TransactionalRequest, bool) error {
			h.lock.Lock()
			defer h.lock.Unlock()
			h.ops = append(h.ops, "save")
			return nil
		})

	fakeRouter := routerfake.New().WithCallReminderFn(func(ctx context.Context, rem *actorapi.Reminder) error {
		if h.reminderGate != nil {
			select {
			case <-h.reminderGate:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		h.lock.Lock()
		defer h.lock.Unlock()
		if h.callReminderErr != nil {
			return h.callReminderErr
		}
		h.ops = append(h.ops, "callReminder:"+rem.Name)
		h.calls = append(h.calls, rem)
		return nil
	})

	actors := fake.New().
		WithReminders(func(context.Context) (actorreminders.Interface, error) {
			return fakeRems, nil
		}).
		WithState(func(context.Context) (actorstate.Interface, error) {
			return fakeState, nil
		}).
		WithRouter(func(context.Context) (router.Interface, error) {
			return fakeRouter, nil
		})

	fact, err := New(t.Context(), Options{
		AppID:             "testapp",
		Namespace:         "default",
		WorkflowActorType: "dapr.internal.default.testapp.workflow",
		ActivityActorType: "dapr.internal.default.testapp.activity",
		ActorTypeBuilder:  common.NewActorTypeBuilder("default"),
		Actors:            actors,
		LocalWakeFastPath: fastPath,
	})
	require.NoError(t, err)

	h.fact = fact.(*factory)
	h.orch = fact.GetOrCreate(instanceID).(*orchestrator)

	return h
}

// primeRunning primes the orchestrator with a running workflow that has an
// outstanding TaskScheduled, so an incoming TaskCompleted takes the normal
// (non-dedup) AddWorkflowEvent path.
func (h *wakeHarness) primeRunning(t *testing.T, instanceID string, scheduled int32) {
	t.Helper()

	startEvent := &protos.HistoryEvent{
		EventId:   -1,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_ExecutionStarted{
			ExecutionStarted: &protos.ExecutionStartedEvent{
				Name:  "TestWorkflow",
				Input: wrapperspb.String(`null`),
				WorkflowInstance: &protos.WorkflowInstance{
					InstanceId: instanceID,
				},
			},
		},
	}
	taskScheduled := &protos.HistoryEvent{
		EventId:   scheduled,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_TaskScheduled{
			TaskScheduled: &protos.TaskScheduledEvent{Name: "act"},
		},
	}
	history := []*backend.HistoryEvent{startEvent, taskScheduled}

	wfState := wfenginestate.NewState(wfenginestate.Options{
		AppID:             "testapp",
		Namespace:         "default",
		WorkflowActorType: "dapr.internal.default.testapp.workflow",
		ActivityActorType: "dapr.internal.default.testapp.activity",
	})
	for _, e := range history {
		wfState.AddToHistory(e)
	}

	h.orch.state = wfState
	h.orch.rstate = runtimestate.NewWorkflowRuntimeState(instanceID, nil, history)
	h.orch.ometa = h.orch.ometaFromState(h.orch.rstate, startEvent.GetExecutionStarted())
}

func taskCompletedEvent(scheduled int32) *backend.HistoryEvent {
	return &protos.HistoryEvent{
		EventId:   -1,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_TaskCompleted{
			TaskCompleted: &protos.TaskCompletedEvent{
				TaskScheduledId: scheduled,
				Result:          wrapperspb.String(`"done"`),
			},
		},
	}
}

func Test_localWake_firesAfterCreateAndDeletesBackstop(t *testing.T) {
	const instanceID = "test-wake-fires"

	h := newWakeHarness(t, instanceID, true)
	h.primeRunning(t, instanceID, 7)

	require.NoError(t, h.orch.addWorkflowEvent(t.Context(), taskCompletedEvent(7)))

	want := []string{"save", "create:new-event-tc-7", "callReminder:new-event-tc-7", "delete:new-event-tc-7"}
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, want, h.snapshotOps())
	}, time.Second*5, time.Millisecond*5,
		"the local wake must fire strictly after save+create and delete the backstop on success")

	h.lock.Lock()
	defer h.lock.Unlock()
	require.Len(t, h.calls, 1)
	rem := h.calls[0]
	assert.Equal(t, "dapr.internal.default.testapp.workflow", rem.ActorType)
	assert.Equal(t, instanceID, rem.ActorID)
	assert.Nil(t, rem.Data, "wake-up reminders carry no payload")
	assert.False(t, rem.SkipLock, "workflow reminders keep the router lock semantics")
}

func Test_localWake_flagOffNoop(t *testing.T) {
	const instanceID = "test-wake-off"

	h := newWakeHarness(t, instanceID, false)
	h.primeRunning(t, instanceID, 7)

	require.NoError(t, h.orch.addWorkflowEvent(t.Context(), taskCompletedEvent(7)))

	time.Sleep(time.Millisecond * 100)
	assert.Equal(t, []string{"save", "create:new-event-tc-7"}, h.snapshotOps(),
		"with the feature off the wake path must not invoke or delete anything")
}

func Test_localWake_startPath(t *testing.T) {
	t.Run("immediate start fires the wake", func(t *testing.T) {
		const instanceID = "test-wake-start"

		h := newWakeHarness(t, instanceID, true)

		ts := time.Now()
		require.NoError(t, h.orch.createWorkflowInstance(t.Context(),
			createRequestBytes(t, startEventFor(instanceID, ts, nil))))

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			ops := h.snapshotOps()
			if assert.Len(c, ops, 4) {
				assert.Equal(c, "save", ops[0])
				assert.Contains(c, ops[1], "create:start-es-")
				assert.Contains(c, ops[2], "callReminder:start-es-")
				assert.Contains(c, ops[3], "delete:start-es-")
			}
		}, time.Second*5, time.Millisecond*5)
	})

	t.Run("delayed start does not fire the wake", func(t *testing.T) {
		const instanceID = "test-wake-delayed"

		h := newWakeHarness(t, instanceID, true)

		start := startEventFor(instanceID, time.Now(), func(es *protos.ExecutionStartedEvent) {
			es.ScheduledStartTimestamp = timestamppb.New(time.Now().Add(time.Hour))
		})
		require.NoError(t, h.orch.createWorkflowInstance(t.Context(), createRequestBytes(t, start)))

		time.Sleep(time.Millisecond * 100)
		ops := h.snapshotOps()
		require.Len(t, ops, 2,
			"a delayed start must keep its scheduler due time: no local wake, no backstop delete")
		assert.Equal(t, "save", ops[0])
		assert.Contains(t, ops[1], "create:start-es-")
	})
}

func Test_localWake_callReminderErrorKeepsBackstop(t *testing.T) {
	const instanceID = "test-wake-err"

	h := newWakeHarness(t, instanceID, true)
	h.callReminderErr = errors.New("wake failed")
	h.primeRunning(t, instanceID, 7)

	require.NoError(t, h.orch.addWorkflowEvent(t.Context(), taskCompletedEvent(7)))

	time.Sleep(time.Millisecond * 100)
	assert.Equal(t, []string{"save", "create:new-event-tc-7"}, h.snapshotOps(),
		"a failed local wake must never delete the backstop; the scheduler drives the turn")
}

func Test_localWake_deleteNotFoundTolerated(t *testing.T) {
	const instanceID = "test-wake-notfound"

	h := newWakeHarness(t, instanceID, true)
	h.deleteErr = status.Error(codes.NotFound, "no such reminder")
	h.primeRunning(t, instanceID, 7)

	require.NoError(t, h.orch.addWorkflowEvent(t.Context(), taskCompletedEvent(7)))

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, []string{"save", "create:new-event-tc-7", "callReminder:new-event-tc-7"}, h.snapshotOps())
	}, time.Second*5, time.Millisecond*5)

	// Drain the goroutine to prove the NotFound did not wedge anything.
	require.NoError(t, h.fact.HaltAll(t.Context()))
}

func Test_localWake_haltAllDrainsGoroutines(t *testing.T) {
	const instanceID = "test-wake-halt"

	h := newWakeHarness(t, instanceID, true)
	h.reminderGate = make(chan struct{})
	h.primeRunning(t, instanceID, 7)

	require.NoError(t, h.orch.addWorkflowEvent(t.Context(), taskCompletedEvent(7)))

	// The wake goroutine is parked on the gate; HaltAll must cancel it and
	// return rather than deadlocking.
	done := make(chan error, 1)
	go func() { done <- h.fact.HaltAll(t.Context()) }()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second * 10):
		t.Fatal("HaltAll did not drain the parked wake goroutine")
	}

	// The cancelled wake must not have deleted the backstop.
	for _, op := range h.snapshotOps() {
		assert.NotContains(t, op, "delete:")
	}

	// The factory keeps serving after HaltAll (placement churn also calls
	// it): a fresh wake context must be in place.
	h.fact.wakeLock.Lock()
	require.NoError(t, h.fact.wakeCtx.Err(), "wake context must be recreated after HaltAll")
	h.fact.wakeLock.Unlock()
}
