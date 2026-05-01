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

	"github.com/dapr/dapr/pkg/placement/internal/loops"
	"github.com/dapr/dapr/pkg/placement/internal/loops/stream"
	"github.com/dapr/dapr/pkg/placement/monitoring"
	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
)

// handleCloseStream handles a close stream request.
func (d *disseminator) handleCloseStream(ctx context.Context, closeStream *loops.ConnCloseStream) {
	s, ok := d.streams[closeStream.StreamIDx]
	if !ok {
		// Ignore old streams.
		return
	}

	monitoring.RecordRuntimesCount(d.connCount.Add(-1), d.namespace)
	if s.hasActors {
		monitoring.RecordActorRuntimesCount(d.actorConnCount.Add(-1), d.namespace)
	}

	// If this stream was already counted towards the current dissemination
	// phase, decrement the counter so the round can still complete without it.
	// Without this, streamsInTargetState would exceed len(d.streams) after
	// deletion.
	if s.currentState == d.currentOperation && d.currentOperation != v1pb.HostOperation_REPORT {
		d.streamsInTargetState--
	}

	delete(d.streams, closeStream.StreamIDx)
	s.loop.Close(&loops.StreamShutdown{
		Error: closeStream.Error,
	})
	stream.StreamLoopFactory.CacheLoop(s.loop)

	if len(d.streams) == 0 {
		return
	}

	// Hosts with no entities were never stored, so there is nothing to remove
	// from the placement table.
	if !d.store.Has(closeStream.StreamIDx) {
		return
	}

	// Always queue the deletion rather than immediately starting a new
	// dissemination round. This allows rapid sequential disconnections (as
	// happen during a rolling update) to be batched together. The queued
	// deletions will be processed when the current dissemination round
	// completes, or when a new connection triggers a round via handleAdd.
	d.waitingToDelete = append(d.waitingToDelete, closeStream.StreamIDx)

	if d.currentOperation != v1pb.HostOperation_REPORT {
		// We're currently disseminating. The store deletion is queued above. Check
		// if removing this stream completes the current phase, this happens when
		// this was the last stream that hadn't responded yet.
		if d.streamsInTargetState >= len(d.streams) {
			d.advancePhase(ctx)
		}
		return
	}

	// Already-armed coalesce timer batches us in.
	if d.coalesceTimer != nil {
		return
	}

	// Arm coalesce on the first delete so a replacement connection
	// arriving in the window folds into one round.
	if d.coalesceWindow > 0 {
		d.startCoalesceTimer()
		return
	}

	d.processWaitingDeletes()
}

// advancePhase checks if all streams have reached the target state and
// advances to the next dissemination phase. This is called from
// handleCloseStream when removing a stream during active dissemination may
// have completed the current phase.
func (d *disseminator) advancePhase(ctx context.Context) {
	if d.streamsInTargetState < len(d.streams) {
		return
	}

	switch d.currentOperation {
	case v1pb.HostOperation_LOCK:
		d.currentOperation = v1pb.HostOperation_UPDATE
		d.streamsInTargetState = 0
		for _, s := range d.streams {
			s.loop.Enqueue(&loops.DisseminateUpdate{
				Version: d.currentVersion,
				Tables:  d.store.PlacementTables(d.currentVersion),
			})
		}

	case v1pb.HostOperation_UPDATE:
		d.currentOperation = v1pb.HostOperation_UNLOCK
		d.streamsInTargetState = 0
		for _, s := range d.streams {
			s.loop.Enqueue(&loops.DisseminateUnlock{
				Version: d.currentVersion,
			})
		}

	case v1pb.HostOperation_UNLOCK:
		d.currentOperation = v1pb.HostOperation_REPORT
		d.streamsInTargetState = 0
		d.timeoutQ.Dequeue(d.currentVersion)
		log.Debugf("Dissemination of version %d in %s complete (via stream close)", d.currentVersion, d.namespace)

		// Mirror handleReportedUnlock's completion path: drain any work
		// queued during this round (deletions and new connections that
		// arrived mid-round). Without this, queued ConnAdd events sit in
		// waitingToDisseminate forever when the round completes via a
		// stream close instead of via reported UNLOCK acks.
		if d.coalesceWindow > 0 && (len(d.waitingToDelete) > 0 || len(d.waitingToDisseminate) > 0) {
			d.startCoalesceTimer()
			return
		}

		if len(d.waitingToDelete) > 0 {
			d.processWaitingDeletes()
			return
		}

		d.processWaitingDisseminate(ctx, nil)
	}
}

// processWaitingDisseminate creates streams for every queued ConnAdd, sets
// them in the store, and either sends one-shot table pushes (no store change)
// or starts a single follow-up dissemination round covering them all. Must
// only be called when currentOperation is REPORT and waitingToDelete has
// already been drained (delete-rounds take priority over add-rounds).
//
// priorChangedTypes is the set of actor types already affected before this
// call (eg, types of streams whose deletion the caller just applied). It is
// merged into the round's ChangedTypes so the LOCK signal carries a complete
// picture. forceRound is implied by len(priorChangedTypes) > 0: when the
// caller mutated the store before invoking us, a round must be emitted even
// if the queued adds themselves do not further change the store.
func (d *disseminator) processWaitingDisseminate(ctx context.Context, priorChangedTypes map[string]struct{}) {
	if len(d.waitingToDisseminate) == 0 {
		return
	}

	waiting := d.waitingToDisseminate
	d.waitingToDisseminate = nil

	if priorChangedTypes == nil {
		priorChangedTypes = make(map[string]struct{})
	}
	storeChanged := len(priorChangedTypes) > 0
	newStreamIDxs := make([]uint64, 0, len(waiting))
	for _, add := range waiting {
		streamIDx := d.addStream(ctx, add)
		newStreamIDxs = append(newStreamIDxs, streamIDx)
		changed, diff := d.store.Set(streamIDx, add.InitialHost)
		if changed {
			storeChanged = true
			for _, t := range diff {
				priorChangedTypes[t] = struct{}{}
			}
		}
	}

	if !storeChanged {
		// All waiting connections had no actors. Send one-shot table pushes
		// only to the newly added streams so they can route actor invocations.
		for _, idx := range newStreamIDxs {
			s, ok := d.streams[idx]
			if !ok || d.store.Has(idx) {
				continue
			}
			version := d.currentVersion
			s.receivingTable = &version
			s.loop.Enqueue(&loops.DisseminateTable{
				Version: d.currentVersion,
				Tables:  d.store.PlacementTables(d.currentVersion),
			})
		}
		return
	}

	d.currentVersion++
	d.timeoutQ.Enqueue(d.currentVersion)
	d.currentOperation = v1pb.HostOperation_LOCK
	d.streamsInTargetState = 0

	changedTypes := setToSortedSlice(priorChangedTypes)
	for _, s := range d.streams {
		s.currentState = v1pb.HostOperation_REPORT
		s.receivingTable = nil
		s.loop.Enqueue(&loops.DisseminateLock{
			Version:      d.currentVersion,
			ChangedTypes: changedTypes,
		})
	}
}

// handleCoalesceFire is the timer callback that drains the post-round
// coalesce window. Called when the disseminator has been idle long enough
// since the last round ended; triggers a single follow-up round covering
// every disconnect / new connection accumulated during the window.
func (d *disseminator) handleCoalesceFire(ctx context.Context) {
	d.coalesceTimer = nil

	// Mirror handleAdd's flow: apply queued deletions to the store BEFORE
	// processing waiting-to-disseminate, so a single round emitted by
	// processWaitingDisseminate captures both. If only deletes are queued
	// (no adds), we still need to start a round for the deletes.
	if len(d.waitingToDelete) > 0 && len(d.waitingToDisseminate) == 0 {
		d.processWaitingDeletes()
		return
	}

	// If deletes are present, applying them mutates the store. We must force
	// a full follow-up round even if the queued adds themselves don't change
	// the store (eg, new connections with no actor entities), otherwise
	// existing streams would never receive the updated placement table
	// reflecting the deletions. Snapshot each deleted host's entity set so
	// the follow-up round's LOCK can advertise those types as changed.
	priorChanged := make(map[string]struct{})
	for _, toDelete := range d.waitingToDelete {
		for _, t := range d.store.EntitiesOf(toDelete) {
			priorChanged[t] = struct{}{}
		}
		d.store.Delete(toDelete)
	}
	d.waitingToDelete = nil

	d.processWaitingDisseminate(ctx, priorChanged)
}

// processWaitingDeletes processes all queued stream deletions in a single
// dissemination round. Must only be called when currentOperation is REPORT.
func (d *disseminator) processWaitingDeletes() {
	if len(d.waitingToDelete) == 0 || len(d.streams) == 0 {
		return
	}

	changedTypeSet := make(map[string]struct{})
	for _, toDelete := range d.waitingToDelete {
		for _, t := range d.store.EntitiesOf(toDelete) {
			changedTypeSet[t] = struct{}{}
		}
		d.store.Delete(toDelete)
	}
	d.waitingToDelete = nil

	d.currentVersion++
	d.timeoutQ.Enqueue(d.currentVersion)
	d.currentOperation = v1pb.HostOperation_LOCK
	d.streamsInTargetState = 0

	changedTypes := setToSortedSlice(changedTypeSet)
	for _, s := range d.streams {
		s.currentState = v1pb.HostOperation_REPORT
		s.receivingTable = nil
		s.loop.Enqueue(&loops.DisseminateLock{
			Version:      d.currentVersion,
			ChangedTypes: changedTypes,
		})
	}
}
