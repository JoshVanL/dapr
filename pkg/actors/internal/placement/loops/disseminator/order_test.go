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

package disseminator

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/internal/placement/loops"
	"github.com/dapr/dapr/pkg/actors/internal/placement/loops/disseminator/inflight"
	"github.com/dapr/dapr/pkg/actors/internal/placement/loops/disseminator/timeout"
	tablefake "github.com/dapr/dapr/pkg/actors/table/fake"
	healthzfake "github.com/dapr/dapr/pkg/healthz/fake"
	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	schedfake "github.com/dapr/dapr/pkg/runtime/scheduler/client/fake"
	loopfake "github.com/dapr/kit/events/loop/fake"
)

// newTestDisseminator creates a disseminator with minimal dependencies for testing
// the handleOrder method.
func newTestDisseminator(t *testing.T) (*disseminator, *healthzfake.Fake, *schedfake.Fake) {
	t.Helper()

	ht := healthzfake.New()
	sched := schedfake.New()

	inf := inflight.New(inflight.Options{
		Hostname: "localhost",
		Port:     "3500",
	})

	streamLoop := loopfake.New[loops.EventStream]()

	dissLoop := LoopFactoryCache.NewLoop(nil)

	diss := &disseminator{
		namespace:        "default",
		id:               "test-id",
		actorTable:       tablefake.New(),
		scheduler:        sched,
		healthTarget:     ht,
		inflight:         inf,
		streamLoop:       streamLoop,
		currentOperation: v1pb.HostOperation_LOCK,
		currentVersion:   0,
		timeout:          time.Second * 30,
		ready:            new(atomic.Bool),
	}

	diss.loop = dissLoop
	diss.timeoutQ = timeout.New(timeout.Options{
		Loop:    dissLoop,
		Timeout: time.Second * 30,
	})

	return diss, ht, sched
}

func TestHandleTimeout(t *testing.T) {
	t.Run("matching version closes stream", func(t *testing.T) {
		diss, _, _ := newTestDisseminator(t)

		var streamClosed bool
		diss.streamLoop = loopfake.New[loops.EventStream]().
			WithClose(func(loops.EventStream) {
				streamClosed = true
			})

		diss.timeoutVersion = 3
		diss.handleTimeout(t.Context(), &loops.DisseminationTimeout{Version: 3})
		assert.True(t, streamClosed, "stream should be closed on matching timeout version")
	})

	t.Run("old version is ignored", func(t *testing.T) {
		diss, _, _ := newTestDisseminator(t)

		var streamClosed bool
		diss.streamLoop = loopfake.New[loops.EventStream]().
			WithClose(func(loops.EventStream) {
				streamClosed = true
			})

		diss.timeoutVersion = 5
		diss.handleTimeout(t.Context(), &loops.DisseminationTimeout{Version: 3})
		assert.False(t, streamClosed, "stream should not be closed for stale timeout version")
	})
}

func TestHandleOrder_UpdateVersionMismatch(t *testing.T) {
	t.Run("update with lower version closes stream", func(t *testing.T) {
		diss, _, _ := newTestDisseminator(t)

		var streamClosed bool
		diss.streamLoop = loopfake.New[loops.EventStream]().
			WithClose(func(loops.EventStream) {
				streamClosed = true
			})

		diss.currentVersion = 10
		err := diss.handleOrder(t.Context(), &loops.StreamOrder{
			Order: &v1pb.PlacementOrder{
				Operation: operationUpdate,
				Version:   5,
			},
		})
		require.NoError(t, err)
		assert.True(t, streamClosed, "stream should be closed on version mismatch")
	})
}

func TestHandleOrder_UnknownOperation(t *testing.T) {
	t.Run("unknown operation closes stream", func(t *testing.T) {
		diss, _, _ := newTestDisseminator(t)

		var streamClosed bool
		diss.streamLoop = loopfake.New[loops.EventStream]().
			WithClose(func(loops.EventStream) {
				streamClosed = true
			})

		err := diss.handleOrder(t.Context(), &loops.StreamOrder{
			Order: &v1pb.PlacementOrder{
				Operation: "invalid",
			},
		})
		require.NoError(t, err)
		assert.True(t, streamClosed, "stream should be closed on unknown operation")
	})
}

func TestHandleTimeout_UpdateDequeuesTimeout(t *testing.T) {
	t.Run("UPDATE arriving before timeout dequeues the timeout", func(t *testing.T) {
		diss, _, _ := newTestDisseminator(t)

		var streamClosed bool
		diss.streamLoop = loopfake.New[loops.EventStream]().
			WithClose(func(loops.EventStream) {
				streamClosed = true
			})

		err := diss.handleOrder(t.Context(), &loops.StreamOrder{
			Order: &v1pb.PlacementOrder{
				Operation: operationLock,
				Version:   5,
			},
		})
		require.NoError(t, err)

		err = diss.handleOrder(t.Context(), &loops.StreamOrder{
			Order: &v1pb.PlacementOrder{
				Operation: operationUpdate,
				Version:   5,
				Tables:    &v1pb.PlacementTables{},
			},
		})
		require.NoError(t, err)

		savedVersion := diss.timeoutVersion - 1
		diss.handleTimeout(t.Context(), &loops.DisseminationTimeout{
			Version: savedVersion,
		})
		assert.False(t, streamClosed, "timeout should be ignored after UPDATE dequeued it")
	})
}

func TestHandleOrder_UnlockVersionMismatch(t *testing.T) {
	t.Run("unlock with lower version is ignored and currentVersion is preserved", func(t *testing.T) {
		diss, ht, _ := newTestDisseminator(t)

		// Simulate a LOCK at version 10.
		err := diss.handleOrder(t.Context(), &loops.StreamOrder{
			Order: &v1pb.PlacementOrder{
				Operation: operationLock,
				Version:   10,
			},
		})
		require.NoError(t, err)
		assert.Equal(t, uint64(10), diss.currentVersion)

		// Simulate an UPDATE at version 10.
		err = diss.handleOrder(t.Context(), &loops.StreamOrder{
			Order: &v1pb.PlacementOrder{
				Operation: operationUpdate,
				Version:   10,
				Tables:    &v1pb.PlacementTables{},
			},
		})
		require.NoError(t, err)
		assert.Equal(t, v1pb.HostOperation_UPDATE, diss.currentOperation)

		// Attempt UNLOCK with version 5 (lower than currentVersion=10).
		// This should be ignored and currentVersion should remain 10.
		err = diss.handleOrder(t.Context(), &loops.StreamOrder{
			Order: &v1pb.PlacementOrder{
				Operation: operationUnlock,
				Version:   5,
			},
		})
		require.NoError(t, err)

		assert.Equal(t, uint64(10), diss.currentVersion,
			"currentVersion should not be updated when unlock version is lower")
		assert.Equal(t, v1pb.HostOperation_UPDATE, diss.currentOperation,
			"operation should remain UPDATE when unlock is ignored")
		assert.False(t, ht.ReadyCalled(),
			"health target should not be marked ready when unlock is ignored")
	})

	t.Run("unlock with matching version succeeds and updates currentVersion", func(t *testing.T) {
		diss, ht, _ := newTestDisseminator(t)

		// LOCK -> UPDATE -> UNLOCK at version 10.
		err := diss.handleOrder(t.Context(), &loops.StreamOrder{
			Order: &v1pb.PlacementOrder{
				Operation: operationLock,
				Version:   10,
			},
		})
		require.NoError(t, err)

		err = diss.handleOrder(t.Context(), &loops.StreamOrder{
			Order: &v1pb.PlacementOrder{
				Operation: operationUpdate,
				Version:   10,
				Tables:    &v1pb.PlacementTables{},
			},
		})
		require.NoError(t, err)

		err = diss.handleOrder(t.Context(), &loops.StreamOrder{
			Order: &v1pb.PlacementOrder{
				Operation: operationUnlock,
				Version:   10,
			},
		})
		require.NoError(t, err)

		assert.Equal(t, uint64(10), diss.currentVersion,
			"currentVersion should be set to matching unlock version")
		assert.Equal(t, v1pb.HostOperation_UNLOCK, diss.currentOperation)
		assert.True(t, ht.ReadyCalled(),
			"health target should be marked ready on successful unlock")
	})

	t.Run("unlock with higher version succeeds and advances currentVersion", func(t *testing.T) {
		diss, ht, _ := newTestDisseminator(t)

		// LOCK -> UPDATE at version 5, UNLOCK at version 10.
		err := diss.handleOrder(t.Context(), &loops.StreamOrder{
			Order: &v1pb.PlacementOrder{
				Operation: operationLock,
				Version:   5,
			},
		})
		require.NoError(t, err)

		err = diss.handleOrder(t.Context(), &loops.StreamOrder{
			Order: &v1pb.PlacementOrder{
				Operation: operationUpdate,
				Version:   5,
				Tables:    &v1pb.PlacementTables{},
			},
		})
		require.NoError(t, err)

		err = diss.handleOrder(t.Context(), &loops.StreamOrder{
			Order: &v1pb.PlacementOrder{
				Operation: operationUnlock,
				Version:   10,
			},
		})
		require.NoError(t, err)

		assert.Equal(t, uint64(10), diss.currentVersion,
			"currentVersion should advance to higher unlock version")
		assert.Equal(t, v1pb.HostOperation_UNLOCK, diss.currentOperation)
		assert.True(t, ht.ReadyCalled())
	})
}

func TestHandleOrder_LegacyPath_NilChangedTypes(t *testing.T) {
	// Old placement server: LOCK arrives without ChangedActorTypes. The
	// daprd must run the legacy path (HaltNonHosted on UPDATE,
	// ReloadActorTypes on UNLOCK), as if the optimization did not exist.
	diss, _, sched := newTestDisseminator(t)
	diss.roundChangedTypes = make(map[string]struct{})

	var haltNonHostedCalls atomic.Int32
	diss.actorTable = tablefake.New().
		WithTypes(func() []string { return []string{"actorA"} }).
		WithHaltNonHosted(func(_ context.Context, _ func(*api.LookupActorRequest) bool) error {
			haltNonHostedCalls.Add(1)
			return nil
		})

	require.NoError(t, diss.handleOrder(t.Context(), &loops.StreamOrder{
		Order: &v1pb.PlacementOrder{Operation: operationLock, Version: 1},
	}))
	assert.True(t, diss.roundAffectsMe, "nil ChangedActorTypes must force legacy path")

	require.NoError(t, diss.handleOrder(t.Context(), &loops.StreamOrder{
		Order: &v1pb.PlacementOrder{
			Operation: operationUpdate, Version: 1, Tables: &v1pb.PlacementTables{},
		},
	}))
	assert.Equal(t, int32(1), haltNonHostedCalls.Load(), "legacy path must call HaltNonHosted")

	require.NoError(t, diss.handleOrder(t.Context(), &loops.StreamOrder{
		Order: &v1pb.PlacementOrder{Operation: operationUnlock, Version: 1},
	}))
	assert.NotNil(t, sched.LastTypes(), "legacy path must call ReloadActorTypes")
}

func TestHandleOrder_NoOpRound_EmptyIntersection(t *testing.T) {
	// New server signals that only "actorA" changed. Local daprd hosts
	// "actorB", so the round is a no-op for us: UPDATE installs the table
	// but skips HaltNonHosted; UNLOCK skips ReloadActorTypes.
	diss, _, sched := newTestDisseminator(t)
	diss.roundChangedTypes = make(map[string]struct{})

	var haltNonHostedCalls atomic.Int32
	diss.actorTable = tablefake.New().
		WithTypes(func() []string { return []string{"actorB"} }).
		WithHaltNonHosted(func(_ context.Context, _ func(*api.LookupActorRequest) bool) error {
			haltNonHostedCalls.Add(1)
			return nil
		})

	require.NoError(t, diss.handleOrder(t.Context(), &loops.StreamOrder{
		Order: &v1pb.PlacementOrder{
			Operation:         operationLock,
			Version:           1,
			ChangedActorTypes: []string{"actorA"},
		},
	}))
	assert.False(t, diss.roundAffectsMe, "no overlap must mark round as no-op")

	require.NoError(t, diss.handleOrder(t.Context(), &loops.StreamOrder{
		Order: &v1pb.PlacementOrder{
			Operation: operationUpdate, Version: 1, Tables: &v1pb.PlacementTables{},
		},
	}))
	assert.Equal(t, int32(0), haltNonHostedCalls.Load(), "no-op round must skip HaltNonHosted")

	require.NoError(t, diss.handleOrder(t.Context(), &loops.StreamOrder{
		Order: &v1pb.PlacementOrder{Operation: operationUnlock, Version: 1},
	}))
	assert.Nil(t, sched.LastTypes(), "no-op round must skip ReloadActorTypes")
	assert.False(t, diss.roundAffectsMe, "roundAffectsMe must reset at UNLOCK")
}

func TestHandleOrder_AffectingRound_TypeOverlap(t *testing.T) {
	// New server signals "actorA" changed; daprd hosts "actorA" too.
	// Legacy path must run.
	diss, _, sched := newTestDisseminator(t)
	diss.roundChangedTypes = make(map[string]struct{})

	var haltNonHostedCalls atomic.Int32
	diss.actorTable = tablefake.New().
		WithTypes(func() []string { return []string{"actorA"} }).
		WithHaltNonHosted(func(_ context.Context, _ func(*api.LookupActorRequest) bool) error {
			haltNonHostedCalls.Add(1)
			return nil
		})

	require.NoError(t, diss.handleOrder(t.Context(), &loops.StreamOrder{
		Order: &v1pb.PlacementOrder{
			Operation:         operationLock,
			Version:           1,
			ChangedActorTypes: []string{"actorA"},
		},
	}))
	assert.True(t, diss.roundAffectsMe)

	require.NoError(t, diss.handleOrder(t.Context(), &loops.StreamOrder{
		Order: &v1pb.PlacementOrder{
			Operation: operationUpdate, Version: 1, Tables: &v1pb.PlacementTables{},
		},
	}))
	assert.Equal(t, int32(1), haltNonHostedCalls.Load())

	require.NoError(t, diss.handleOrder(t.Context(), &loops.StreamOrder{
		Order: &v1pb.PlacementOrder{Operation: operationUnlock, Version: 1},
	}))
	assert.NotNil(t, sched.LastTypes())
}

func TestHandleOrder_CompressedRound_LockNoOpThenAffecting(t *testing.T) {
	// LOCK n with no overlap, then LOCK n+1 with overlap before UPDATE.
	// roundAffectsMe sticks at true so UPDATE n+1 runs the legacy path.
	diss, _, _ := newTestDisseminator(t)
	diss.roundChangedTypes = make(map[string]struct{})

	var haltNonHostedCalls atomic.Int32
	diss.actorTable = tablefake.New().
		WithTypes(func() []string { return []string{"actorA"} }).
		WithHaltNonHosted(func(_ context.Context, _ func(*api.LookupActorRequest) bool) error {
			haltNonHostedCalls.Add(1)
			return nil
		})

	require.NoError(t, diss.handleOrder(t.Context(), &loops.StreamOrder{
		Order: &v1pb.PlacementOrder{
			Operation:         operationLock,
			Version:           1,
			ChangedActorTypes: []string{"actorB"},
		},
	}))
	assert.False(t, diss.roundAffectsMe)

	require.NoError(t, diss.handleOrder(t.Context(), &loops.StreamOrder{
		Order: &v1pb.PlacementOrder{
			Operation:         operationLock,
			Version:           2,
			ChangedActorTypes: []string{"actorA"},
		},
	}))
	assert.True(t, diss.roundAffectsMe, "second LOCK with overlap must flip to legacy path")

	require.NoError(t, diss.handleOrder(t.Context(), &loops.StreamOrder{
		Order: &v1pb.PlacementOrder{
			Operation: operationUpdate, Version: 2, Tables: &v1pb.PlacementTables{},
		},
	}))
	assert.Equal(t, int32(1), haltNonHostedCalls.Load())
}
