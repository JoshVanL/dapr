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

package state

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/dapr/dapr/pkg/actors/api"
	statefake "github.com/dapr/dapr/pkg/actors/state/fake"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/kit/ptr"
)

// signatureUpserts returns every signature-row upsert in the request, keyed by
// state key.
func signatureUpserts(t *testing.T, s *State, actorID string) map[string]api.TransactionalUpsert {
	t.Helper()
	req, err := s.GetSaveRequest(actorID)
	require.NoError(t, err)

	out := make(map[string]api.TransactionalUpsert)
	for _, op := range req.Operations {
		if op.Operation != api.Upsert {
			continue
		}
		u, ok := op.Request.(api.TransactionalUpsert)
		if !ok || len(u.Key) < len(signatureKeyPrefix) || u.Key[:len(signatureKeyPrefix)] != signatureKeyPrefix {
			continue
		}
		out[u.Key] = u
	}
	return out
}

// persistedSignedState builds a state that mimics a loaded/saved workflow
// with one persisted signature and known ETags.
func persistedSignedState(t *testing.T) *State {
	t.Helper()
	s := NewState(testOpts())
	s.AddToHistory(testEvent(0))
	addSig(t, s, &backend.HistorySignature{StartEventIndex: 0, EventCount: 1, Signature: []byte("sig-data")})
	s.ResetChangeTracking()
	s.SetMetadataETag(ptr.Of("meta-v1"))
	s.SetHeadSignatureETag(ptr.Of("sig-v1"))
	return s
}

func TestGetSaveRequest_ChainHeadGuard_InboxOnlySave(t *testing.T) {
	t.Parallel()

	s := persistedSignedState(t)
	s.AddToInbox(testEvent(1))

	ups := signatureUpserts(t, s, "actor1")
	guard, ok := ups["signature-000000"]
	require.True(t, ok, "expected chain-head guard upsert on inbox-only save")
	require.NotNil(t, guard.ETag)
	assert.Equal(t, "sig-v1", *guard.ETag)
	assert.Equal(t, s.RawSignatures[0], guard.Value, "guard must rewrite the exact persisted bytes")
}

func TestGetSaveRequest_ChainHeadGuard_NewSignatureSave(t *testing.T) {
	t.Parallel()

	s := persistedSignedState(t)
	s.AddToHistory(testEvent(1))
	addSig(t, s, &backend.HistorySignature{StartEventIndex: 1, EventCount: 1, Signature: []byte("sig-data-2")})

	ups := signatureUpserts(t, s, "actor1")
	guard, ok := ups["signature-000000"]
	require.True(t, ok, "expected chain-head guard upsert on the previous head")
	require.NotNil(t, guard.ETag)
	assert.Equal(t, "sig-v1", *guard.ETag)

	newSig, ok := ups["signature-000001"]
	require.True(t, ok, "expected upsert for the new signature")
	assert.Nil(t, newSig.ETag, "new signature rows carry no ETag")
}

func TestGetSaveRequest_ChainHeadGuard_AbsentOnFirstSignature(t *testing.T) {
	t.Parallel()

	s := NewState(testOpts())
	s.AddToHistory(testEvent(0))
	addSig(t, s, &backend.HistorySignature{StartEventIndex: 0, EventCount: 1, Signature: []byte("sig-data")})

	ups := signatureUpserts(t, s, "actor1")
	first, ok := ups["signature-000000"]
	require.True(t, ok)
	assert.Nil(t, first.ETag, "first signature has no prior head to guard")
}

func TestGetSaveRequest_ChainHeadGuard_AbsentWithoutETag(t *testing.T) {
	t.Parallel()

	s := persistedSignedState(t)
	s.SetHeadSignatureETag(nil)
	s.AddToInbox(testEvent(1))

	ups := signatureUpserts(t, s, "actor1")
	assert.Empty(t, ups, "no guard upsert expected when no head ETag is known")
}

func TestGetSaveRequest_ChainHeadGuard_AbsentAfterContinueAsNew(t *testing.T) {
	t.Parallel()

	s := persistedSignedState(t)
	s.Reset()
	assert.Nil(t, s.HeadSignatureETag(), "Reset must clear the head signature ETag")

	s.AddToHistory(testEvent(0))
	addSig(t, s, &backend.HistorySignature{StartEventIndex: 0, EventCount: 1, Signature: []byte("sig-after-can")})

	ups := signatureUpserts(t, s, "actor1")
	sig, ok := ups["signature-000000"]
	require.True(t, ok)
	assert.Nil(t, sig.ETag, "no guard expected after a chain reset")
}

func TestGetSaveRequest_ChainHeadGuard_AbsentOnTombstoneSave(t *testing.T) {
	t.Parallel()

	s := persistedSignedState(t)
	s.AddToHistory(tamperMarkerEvent())

	ups := signatureUpserts(t, s, "actor1")
	assert.Empty(t, ups, "a tombstone save must not carry the chain-head guard")
}

func TestLoadWorkflowState_CapturesHeadSignatureETag(t *testing.T) {
	t.Parallel()

	const actorID = "wf-head-etag"

	sigBytes, err := proto.MarshalOptions{Deterministic: true}.Marshal(&backend.HistorySignature{
		StartEventIndex: 0,
		EventCount:      2,
		Signature:       []byte("sig-data"),
	})
	require.NoError(t, err)
	histBytes, err := proto.Marshal(testEvent(0))
	require.NoError(t, err)
	// End the history with a tamper marker so the load path returns without
	// requiring a configured signer for the signed rows.
	markerBytes, err := proto.Marshal(tamperMarkerEvent())
	require.NoError(t, err)

	metaBytes, err := proto.Marshal(&backend.BackendWorkflowStateMetadata{
		HistoryLength:   2,
		SignatureLength: 1,
		Generation:      1,
	})
	require.NoError(t, err)

	bulk := map[string]api.BulkStateEntry{
		"history-000000":   {Data: histBytes},
		"history-000001":   {Data: markerBytes},
		"signature-000000": {Data: sigBytes, ETag: ptr.Of("sig-etag-7")},
	}

	store := statefake.New().
		WithGetFn(func(_ context.Context, req *api.GetStateRequest, _ bool) (*api.StateResponse, error) {
			if req.Key == MetadataKey {
				return &api.StateResponse{Data: metaBytes, ETag: ptr.Of("meta-etag")}, nil
			}
			return &api.StateResponse{}, nil
		}).
		WithGetBulkFn(func(_ context.Context, req *api.GetBulkStateRequest, _ bool) (api.BulkStateResponse, error) {
			out := api.BulkStateResponse{}
			for _, k := range req.Keys {
				out[k] = bulk[k]
			}
			return out, nil
		})

	got, err := LoadWorkflowState(t.Context(), store, actorID, Options{
		WorkflowActorType: "workflow",
		ActivityActorType: "activity",
	})
	require.NoError(t, err)
	require.NotNil(t, got)

	require.NotNil(t, got.HeadSignatureETag())
	assert.Equal(t, "sig-etag-7", *got.HeadSignatureETag())
}
