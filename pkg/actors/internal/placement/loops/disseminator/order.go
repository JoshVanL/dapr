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
	"errors"
	"fmt"
	"strconv"

	"github.com/dapr/dapr/pkg/actors/internal/placement/loops"
	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
)

const (
	operationLock   = "lock"
	operationUpdate = "update"
	operationUnlock = "unlock"
)

func (d *disseminator) handleLookupRequest(req *loops.LookupRequest) {
	d.inflight.AcquireLookup(req)
}

func (d *disseminator) handleAcquireRequest(req *loops.LockRequest) {
	d.inflight.Acquire(req)
}

// lockAffectsLocalTypes returns true when this daprd should run the legacy
// drain/halt/reload path for the round being signaled by LOCK. nil means
// the server is older than the type-aware-LOCK rollout and did not advertise
// the changed-types set; in that case we must run the legacy path. An empty
// slice from a new server means a no-op round (unreachable in practice; the
// server only emits LOCK when something changed). A non-empty slice with no
// overlap against locally hosted types means the round does not affect us.
func (d *disseminator) lockAffectsLocalTypes(changed []string) bool {
	if changed == nil {
		return true
	}
	if len(changed) == 0 {
		return false
	}
	hosted := make(map[string]struct{})
	for _, t := range d.actorTable.Types() {
		hosted[t] = struct{}{}
	}
	for _, t := range changed {
		if _, ok := hosted[t]; ok {
			return true
		}
	}
	return false
}

func (d *disseminator) handleReportHost(report *loops.ReportHost) {
	report.Host.Version = nil
	report.Host.Operation = v1pb.HostOperation_REPORT
	d.streamLoop.Enqueue(&loops.StreamSend{
		Host: report.Host,
	})
}

func (d *disseminator) handleOrder(ctx context.Context, order *loops.StreamOrder) error {
	var version uint64
	if v := order.Order.GetVersion(); v > 0 {
		version = v
	} else {
		//nolint:staticcheck
		version, _ = strconv.ParseUint(order.Order.GetTables().GetVersion(), 10, 64)
	}

	log.Debugf("Handling placement order=%s version=%d", order.Order.GetOperation(), version)

	switch order.Order.GetOperation() {
	case operationLock:
		// LOCK signals that the placement server is about to push a new
		// table. The new tables haven't arrived yet, so we don't know
		// which actor types will change. Lookups continue to resolve
		// against the current table; per-type queueing only kicks in at
		// UPDATE once the diff is known.
		//
		// If the server advertised which types changed, intersect against
		// the locally hosted set. An empty intersection means the round
		// is a no-op for us and UPDATE/UNLOCK can skip the loop-blocking
		// drain/halt/reload work. roundAffectsMe is sticky-OR'd so a
		// follow-up LOCK in a compressed round (LOCK n no-op + LOCK n+1
		// affects-me) flips the round into the legacy path before its
		// UPDATE arrives.
		d.roundAffectsMe = d.roundAffectsMe || d.lockAffectsLocalTypes(order.Order.GetChangedActorTypes())

		d.timeoutQ.Dequeue(d.timeoutVersion)
		d.timeoutVersion++
		d.timeoutQ.Enqueue(d.timeoutVersion)

		d.currentOperation = v1pb.HostOperation_LOCK
		d.currentVersion = version

		d.streamLoop.Enqueue(&loops.StreamSend{
			Host: &v1pb.Host{
				Operation: v1pb.HostOperation_LOCK,
				Version:   new(d.currentVersion),
				Namespace: d.namespace,
				Id:        d.id,
			},
		})

	case operationUpdate:
		if d.currentVersion > version {
			d.streamLoop.Close(&loops.Shutdown{
				Error: fmt.Errorf("version mismatch: expected %d, got %d",
					d.currentVersion,
					version,
				),
			})
			return nil
		}

		d.timeoutQ.Dequeue(d.timeoutVersion)
		d.currentOperation = v1pb.HostOperation_UPDATE

		if !d.roundAffectsMe {
			// No changed type is hosted locally. Install the new table
			// for cross-host routing correctness, ack, and let the
			// disseminator loop continue serving lookups without running
			// the per-type drain or HaltNonHosted walk that would
			// otherwise stall every interleaved LookupRequest until the
			// drain timeout expires.
			d.inflight.InstallTable(order.Order.GetTables(), version)
			d.inflight.Open(ctx)
			d.streamLoop.Enqueue(&loops.StreamSend{
				Host: &v1pb.Host{
					Operation: v1pb.HostOperation_UPDATE,
					Version:   new(d.currentVersion),
					Namespace: d.namespace,
					Id:        d.id,
				},
			})
			break
		}

		// Diff old vs new tables, install new tables, and block only the
		// actor types whose hash ring actually changed. Open ensures the
		// claim-tracking loop is running and flushes any queued requests
		// for unchanged types. Accumulate into roundChangedTypes so a
		// later UNLOCK releases every type touched across compressed
		// rounds (the placement server may elide intermediate UNLOCKs).
		changed := d.inflight.Set(order.Order.GetTables(), version)
		for _, t := range changed {
			d.roundChangedTypes[t] = struct{}{}
		}
		d.inflight.LockTypes(changed)
		d.inflight.Open(ctx)

		// Drain in-flight claims for actor types whose hash ring changed
		// in this UPDATE so the request layer can retry against the new
		// routing. Claims for unchanged types survive: a routine
		// dissemination round is no longer fatal to in-flight invocations
		// of unaffected types.
		d.inflight.CancelClaimsForTypes(changed, errors.New("placement table updated"))

		if err := d.actorTable.HaltNonHosted(ctx, d.inflight.IsActorHostedNoLock); err != nil {
			log.Errorf("Error draining non-hosted actors: %s", err)
		}

		d.streamLoop.Enqueue(&loops.StreamSend{
			Host: &v1pb.Host{
				Operation: v1pb.HostOperation_UPDATE,
				Version:   new(d.currentVersion),
				Namespace: d.namespace,
				Id:        d.id,
			},
		})

	case operationUnlock:
		if d.currentOperation != v1pb.HostOperation_UPDATE {
			log.Warnf("Invalid operation sequence: expected UPDATE before UNLOCK, ignoring unlock")
			return nil
		}

		if d.currentVersion > version {
			log.Errorf("Version mismatch: expected %d, got %d, ignoring unlock",
				d.currentVersion,
				version,
			)
			return nil
		}
		d.currentVersion = version
		d.currentOperation = v1pb.HostOperation_UNLOCK

		if !d.roundAffectsMe {
			log.Debugf("Dissemination complete for version %d (no locally hosted types affected), short-circuiting on disseminator %s/%s",
				version, d.namespace, d.id,
			)
		} else {
			// Release every type accumulated since the last UNLOCK. This
			// covers the compressed-rounds case where the placement server
			// emitted multiple LOCK+UPDATE pairs before a single trailing
			// UNLOCK.
			toUnlock := make([]string, 0, len(d.roundChangedTypes))
			for t := range d.roundChangedTypes {
				toUnlock = append(toUnlock, t)
			}

			log.Infof("Dissemination complete for version %d (changed types %v), unlocking disseminator %s/%s",
				version, toUnlock, d.namespace, d.id,
			)

			d.scheduler.ReloadActorTypes(d.actorTable.Types())
			d.inflight.UnlockTypes(toUnlock)
		}

		clear(d.roundChangedTypes)
		d.roundAffectsMe = false

		d.streamLoop.Enqueue(&loops.StreamSend{
			Host: &v1pb.Host{
				Operation: v1pb.HostOperation_UNLOCK,
				Version:   new(d.currentVersion),
				Namespace: d.namespace,
				Id:        d.id,
			},
		})

		d.healthTarget.Ready()
		d.ready.Store(true)

	default:
		d.streamLoop.Close(&loops.Shutdown{
			Error: fmt.Errorf("unknown operation: %s", order.Order.GetOperation()),
		})
		return nil
	}

	return nil
}
