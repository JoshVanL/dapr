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
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	actorapi "github.com/dapr/dapr/pkg/actors/api"
	remindersfake "github.com/dapr/dapr/pkg/actors/reminders/fake"
	statefake "github.com/dapr/dapr/pkg/actors/state/fake"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/orchestrator/signing"
	wfenginestate "github.com/dapr/dapr/pkg/runtime/wfengine/state"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
)

const auditTestInstanceID = "audit-test-workflow"

const auditTestETag = "etag1"

func auditTestHistoryEvent() *backend.HistoryEvent {
	return &backend.HistoryEvent{
		EventId:   -1,
		Timestamp: timestamppb.New(time.Unix(1755000000, 0)),
		EventType: &protos.HistoryEvent_ExecutionStarted{
			ExecutionStarted: &protos.ExecutionStartedEvent{
				Name: "TestWorkflow",
				WorkflowInstance: &protos.WorkflowInstance{
					InstanceId: auditTestInstanceID,
				},
			},
		},
	}
}

func auditTestCompletedEvent() *backend.HistoryEvent {
	return &backend.HistoryEvent{
		EventId:   -1,
		Timestamp: timestamppb.New(time.Unix(1755000001, 0)),
		EventType: &protos.HistoryEvent_ExecutionCompleted{
			ExecutionCompleted: &protos.ExecutionCompletedEvent{
				WorkflowStatus: protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED,
			},
		},
	}
}

func auditTestMetadataBytes(t *testing.T, historyLen, signatureLen uint64) []byte {
	t.Helper()
	data, err := proto.Marshal(&backend.BackendWorkflowStateMetadata{
		HistoryLength:   historyLen,
		SignatureLength: signatureLen,
		Generation:      1,
	})
	require.NoError(t, err)
	return data
}

// newAuditTestOrchestrator builds a resident orchestrator with a cached state
// holding one history event and the given metadata ETag, backed by the given
// fakes.
func newAuditTestOrchestrator(t *testing.T, actorState *statefake.Fake, reminders *remindersfake.Fake, etag *string) *orchestrator {
	t.Helper()

	state := wfenginestate.NewState(wfenginestate.Options{
		AppID:             "testapp",
		Namespace:         "default",
		WorkflowActorType: "workflow",
		ActivityActorType: "activity",
	})
	state.AddToHistory(auditTestHistoryEvent())
	state.ResetChangeTracking()
	state.SetMetadataETag(etag)

	o := newOrchestrator()
	o.factory = &factory{
		appID:             "testapp",
		namespace:         "default",
		actorType:         "workflow",
		activityActorType: "activity",
		actorState:        actorState,
		reminders:         reminders,
	}
	o.actorID = auditTestInstanceID
	o.state = state
	o.signing = &signing.Signing{
		Namespace:         "default",
		ActorID:           auditTestInstanceID,
		ActorType:         "workflow",
		ActivityActorType: "activity",
		Reminders:         reminders,
	}

	return o
}

// auditTestStore returns state fakes serving one unsigned history event under
// the given metadata ETag, counting metadata Gets.
func auditTestStore(t *testing.T, storeETag string, getCount *atomic.Int64) *statefake.Fake {
	t.Helper()
	metaBytes := auditTestMetadataBytes(t, 1, 0)
	historyBytes, err := proto.Marshal(auditTestHistoryEvent())
	require.NoError(t, err)

	return statefake.New().
		WithGetFn(func(_ context.Context, req *actorapi.GetStateRequest, _ bool) (*actorapi.StateResponse, error) {
			if getCount != nil {
				getCount.Add(1)
			}
			et := storeETag
			return &actorapi.StateResponse{Data: metaBytes, ETag: &et}, nil
		}).
		WithGetBulkFn(func(_ context.Context, _ *actorapi.GetBulkStateRequest, _ bool) (actorapi.BulkStateResponse, error) {
			return actorapi.BulkStateResponse{
				"history-000000": {Data: historyBytes},
			}, nil
		})
}

func Test_auditIntegrity_skipsWhenNoCachedState(t *testing.T) {
	t.Parallel()

	var gets atomic.Int64
	o := newAuditTestOrchestrator(t, auditTestStore(t, auditTestETag, &gets), remindersfake.New(), nil)
	o.state = nil

	o.AuditIntegrity(t.Context())

	assert.Equal(t, int64(0), gets.Load(), "no store read expected when nothing is cached")
	assert.Nil(t, o.state)
}

func Test_auditIntegrity_skipsWhenCompleted(t *testing.T) {
	t.Parallel()

	var gets atomic.Int64
	etag := auditTestETag
	o := newAuditTestOrchestrator(t, auditTestStore(t, etag, &gets), remindersfake.New(), &etag)
	o.state.AddToHistory(auditTestCompletedEvent())

	o.AuditIntegrity(t.Context())

	assert.Equal(t, int64(0), gets.Load(), "no store read expected for a terminal workflow")
	assert.NotNil(t, o.state, "cache must be retained")
}

func Test_auditIntegrity_skipsWhenNoMetadataETag(t *testing.T) {
	t.Parallel()

	var gets atomic.Int64
	o := newAuditTestOrchestrator(t, auditTestStore(t, auditTestETag, &gets), remindersfake.New(), nil)

	o.AuditIntegrity(t.Context())

	assert.Equal(t, int64(0), gets.Load(), "no store read expected without a version anchor")
	assert.NotNil(t, o.state, "cache must be retained")
}

func Test_auditIntegrity_skipsWhenLockHeld(t *testing.T) {
	t.Parallel()

	var gets atomic.Int64
	etag := auditTestETag
	o := newAuditTestOrchestrator(t, auditTestStore(t, etag, &gets), remindersfake.New(), &etag)

	unlock, err := o.lock.ContextLock(t.Context())
	require.NoError(t, err)
	defer unlock()

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	o.AuditIntegrity(ctx)

	assert.Equal(t, int64(0), gets.Load(), "no store read expected when the actor stays busy")
	assert.NotNil(t, o.state, "cache must be retained")
}

func Test_auditIntegrity_verifiedWhenStoreMatches(t *testing.T) {
	t.Parallel()

	var gets atomic.Int64
	etag := auditTestETag
	o := newAuditTestOrchestrator(t, auditTestStore(t, etag, &gets), remindersfake.New(), &etag)
	cached := o.state

	o.AuditIntegrity(t.Context())

	assert.Positive(t, gets.Load(), "store must have been read")
	assert.Same(t, cached, o.state, "matching state must not invalidate the cache")
}

func Test_auditIntegrity_divergentOnETagMismatch(t *testing.T) {
	t.Parallel()

	etag := auditTestETag
	o := newAuditTestOrchestrator(t, auditTestStore(t, "etag2", nil), remindersfake.New(), &etag)
	cached := o.state

	o.AuditIntegrity(t.Context())

	require.NotNil(t, o.state, "divergence must reload the cache from the store")
	assert.NotSame(t, cached, o.state, "the stale cache must have been replaced")
	require.NotNil(t, o.state.MetadataETag())
	assert.Equal(t, "etag2", *o.state.MetadataETag(), "the reloaded cache must carry the store's version")
	assert.False(t, o.state.HasTamperMarker(), "a verifying divergent state must not be tombstoned")
}

func Test_auditIntegrity_divergentOnContentMismatch(t *testing.T) {
	t.Parallel()

	metaBytes := auditTestMetadataBytes(t, 2, 0)
	historyBytes, err := proto.Marshal(auditTestHistoryEvent())
	require.NoError(t, err)
	completedBytes, err := proto.Marshal(auditTestCompletedEvent())
	require.NoError(t, err)

	etag := auditTestETag
	actorState := statefake.New().
		WithGetFn(func(_ context.Context, _ *actorapi.GetStateRequest, _ bool) (*actorapi.StateResponse, error) {
			et := etag
			return &actorapi.StateResponse{Data: metaBytes, ETag: &et}, nil
		}).
		WithGetBulkFn(func(_ context.Context, _ *actorapi.GetBulkStateRequest, _ bool) (actorapi.BulkStateResponse, error) {
			return actorapi.BulkStateResponse{
				"history-000000": {Data: historyBytes},
				"history-000001": {Data: completedBytes},
			}, nil
		})

	o := newAuditTestOrchestrator(t, actorState, remindersfake.New(), &etag)
	cached := o.state

	o.AuditIntegrity(t.Context())

	require.NotNil(t, o.state, "divergence must reload the cache from the store")
	assert.NotSame(t, cached, o.state, "the stale cache must have been replaced")
	assert.Len(t, o.state.History, 2, "the reloaded cache must hold the store's history")
}

func Test_auditIntegrity_divergentWhenStateDeleted(t *testing.T) {
	t.Parallel()

	etag := auditTestETag
	actorState := statefake.New().WithGetFn(func(_ context.Context, _ *actorapi.GetStateRequest, _ bool) (*actorapi.StateResponse, error) {
		return &actorapi.StateResponse{}, nil
	})
	o := newAuditTestOrchestrator(t, actorState, remindersfake.New(), &etag)

	o.AuditIntegrity(t.Context())

	assert.Nil(t, o.state, "deleted persisted state must drop the cache")
}

func Test_auditIntegrity_skipsWhenTurnIntervened(t *testing.T) {
	t.Parallel()

	metaBytes := auditTestMetadataBytes(t, 1, 0)
	historyBytes, err := proto.Marshal(auditTestHistoryEvent())
	require.NoError(t, err)

	etag := auditTestETag
	newETag := "etag2"
	var o *orchestrator
	actorState := statefake.New().
		WithGetFn(func(_ context.Context, _ *actorapi.GetStateRequest, _ bool) (*actorapi.StateResponse, error) {
			et := etag
			return &actorapi.StateResponse{Data: metaBytes, ETag: &et}, nil
		}).
		WithGetBulkFn(func(_ context.Context, _ *actorapi.GetBulkStateRequest, _ bool) (actorapi.BulkStateResponse, error) {
			// Simulate a turn saving under a new metadata version while the
			// audit performs its unlocked store read.
			o.state.SetMetadataETag(&newETag)
			return actorapi.BulkStateResponse{
				"history-000000": {Data: historyBytes},
			}, nil
		})

	o = newAuditTestOrchestrator(t, actorState, remindersfake.New(), &etag)

	o.AuditIntegrity(t.Context())

	assert.NotNil(t, o.state, "cache must be retained when a turn intervened")
	require.NotNil(t, o.state.MetadataETag())
	assert.Equal(t, newETag, *o.state.MetadataETag(), "the intervening turn's anchor must be untouched")
}

func Test_auditIntegrity_tombstonesOnConfirmedTamper(t *testing.T) {
	t.Parallel()

	// Metadata declares one signature but the row is absent from the store:
	// LoadWorkflowState returns a VerificationError with the metadata row
	// unchanged, which is the confirmed-tamper shape.
	metaBytes := auditTestMetadataBytes(t, 1, 1)
	historyBytes, err := proto.Marshal(auditTestHistoryEvent())
	require.NoError(t, err)

	etag := auditTestETag
	var saves atomic.Int64
	actorState := statefake.New().
		WithGetFn(func(_ context.Context, _ *actorapi.GetStateRequest, _ bool) (*actorapi.StateResponse, error) {
			et := etag
			return &actorapi.StateResponse{Data: metaBytes, ETag: &et}, nil
		}).
		WithGetBulkFn(func(_ context.Context, _ *actorapi.GetBulkStateRequest, _ bool) (actorapi.BulkStateResponse, error) {
			return actorapi.BulkStateResponse{
				"history-000000": {Data: historyBytes},
			}, nil
		}).
		WithTransactionalStateOperationFn(func(_ context.Context, _ bool, _ *actorapi.TransactionalRequest, _ bool) error {
			saves.Add(1)
			return nil
		})

	var reminderDeletes atomic.Int64
	reminders := remindersfake.New().WithDeleteByActorID(func(_ context.Context, _ *actorapi.DeleteRemindersByActorIDRequest) error {
		reminderDeletes.Add(1)
		return nil
	})

	o := newAuditTestOrchestrator(t, actorState, reminders, &etag)

	o.AuditIntegrity(t.Context())

	require.NotNil(t, o.state, "tombstone must refresh the cache with the failed state")
	assert.True(t, o.state.HasTamperMarker(), "state must carry the tamper marker")
	assert.Equal(t, int64(1), saves.Load(), "tamper marker must be persisted")
	assert.Equal(t, int64(2), reminderDeletes.Load(), "workflow and activity reminders must be deleted")
}

func Test_auditIntegrity_reloadsOnDivergentVerificationFailure(t *testing.T) {
	t.Parallel()

	// Same missing-signature tamper shape, but the store's metadata version no
	// longer matches the audit snapshot. The audit must not tombstone from the
	// unlocked read; it reloads through the serialized cold-load path, which
	// re-detects the tamper and tombstones there.
	metaBytes := auditTestMetadataBytes(t, 1, 1)
	historyBytes, err := proto.Marshal(auditTestHistoryEvent())
	require.NoError(t, err)

	cachedETag := auditTestETag
	storeETag := "etag2"
	actorState := statefake.New().
		WithGetFn(func(_ context.Context, _ *actorapi.GetStateRequest, _ bool) (*actorapi.StateResponse, error) {
			et := storeETag
			return &actorapi.StateResponse{Data: metaBytes, ETag: &et}, nil
		}).
		WithGetBulkFn(func(_ context.Context, _ *actorapi.GetBulkStateRequest, _ bool) (actorapi.BulkStateResponse, error) {
			return actorapi.BulkStateResponse{
				"history-000000": {Data: historyBytes},
			}, nil
		})

	o := newAuditTestOrchestrator(t, actorState, remindersfake.New(), &cachedETag)

	o.AuditIntegrity(t.Context())

	require.NotNil(t, o.state, "reload must repopulate the cache")
	assert.True(t, o.state.HasTamperMarker(), "the serialized reload must have tombstoned the tampered state")
}
