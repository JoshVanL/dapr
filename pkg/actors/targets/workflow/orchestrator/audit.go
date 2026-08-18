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
	"bytes"
	"context"
	"errors"
	"time"

	"github.com/dapr/dapr/pkg/actors/targets/workflow/orchestrator/audit"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	wfenginestate "github.com/dapr/dapr/pkg/runtime/wfengine/state"
	wferrors "github.com/dapr/dapr/pkg/runtime/wfengine/state/errors"
)

// auditLockTimeout bounds how long an audit waits for the actor's lock. An
// actor that stays busy past this is skipped for the cycle and retried on the
// next sweep.
const auditLockTimeout = 5 * time.Second

// auditTargets snapshots the currently resident orchestrators for the
// background integrity auditor.
func (f *factory) auditTargets() []audit.Target {
	var targets []audit.Target
	f.table.Range(func(_, o any) bool {
		targets = append(targets, o.(*orchestrator))
		return true
	})
	return targets
}

// AuditIntegrity re-reads this actor's workflow state from the state store and
// verifies it against both the signature chain and the in-memory cache.
// Implements [audit.Target]. It runs in three phases so the store read never
// happens under the actor lock:
//
//  1. Briefly take the lock and snapshot the cached metadata ETag.
//  2. Unlocked, load and verify the state from the store.
//  3. Briefly take the lock again and, if no turn intervened (the cached ETag
//     is unchanged), classify the result and act on it.
//
// A confirmed verification failure under an unchanged metadata row is
// tombstoned directly. A verification failure with a changed metadata row is
// re-checked through the serialized cold-load path, which has its own
// torn-read retry, so a concurrent save or continue-as-new can never cause a
// false tombstone.
func (o *orchestrator) AuditIntegrity(ctx context.Context) {
	start := time.Now()

	lockCtx, cancel := context.WithTimeout(ctx, auditLockTimeout)
	unlock, err := o.lock.ContextLock(lockCtx)
	cancel()
	if err != nil {
		o.recordAudit(ctx, diag.IntegrityAuditSkipped, start)
		return
	}

	if o.state == nil || o.state.IsCompleted() || o.state.MetadataETag() == nil {
		unlock()
		o.recordAudit(ctx, diag.IntegrityAuditSkipped, start)
		return
	}
	etagBefore := *o.state.MetadataETag()
	opts := wfenginestate.Options{
		AppID:             o.appID,
		Namespace:         o.namespace,
		WorkflowActorType: o.actorType,
		ActivityActorType: o.activityActorType,
		Signer:            o.signer,
	}
	unlock()

	st, loadErr := wfenginestate.LoadWorkflowState(ctx, o.actorState, o.actorID, opts)

	lockCtx, cancel = context.WithTimeout(ctx, auditLockTimeout)
	unlock, err = o.lock.ContextLock(lockCtx)
	cancel()
	if err != nil {
		o.recordAudit(ctx, diag.IntegrityAuditSkipped, start)
		return
	}
	defer unlock()

	// A turn intervened between the phases if the cache was invalidated or
	// saved under a new metadata version. Either way the tampering surface has
	// been re-anchored by that turn (a save presented the ETag to the store, a
	// reload re-verified the chain), so this cycle has nothing to add.
	if o.state == nil || o.state.MetadataETag() == nil || *o.state.MetadataETag() != etagBefore {
		o.recordAudit(ctx, diag.IntegrityAuditSkipped, start)
		return
	}

	var verifyErr *wferrors.VerificationError
	switch {
	case errors.As(loadErr, &verifyErr):
		if st != nil && st.MetadataETag() != nil && *st.MetadataETag() == etagBefore && !st.IsCompleted() {
			// The metadata row is untouched since our snapshot, so the failure
			// cannot be a torn read against a concurrent save: the entry rows
			// were tampered with underneath an idle metadata row.
			log.Errorf("Workflow actor '%s': background audit detected state store tampering: %s", o.actorID, loadErr)
			if _, _, terr := o.tombstoneTamperedState(ctx, opts, st, loadErr); terr != nil {
				log.Errorf("Workflow actor '%s': failed to tombstone tampered state from audit: %s", o.actorID, terr)
				o.recordAudit(ctx, diag.IntegrityAuditError, start)
				return
			}
			o.recordAudit(ctx, diag.IntegrityAuditTampered, start)
			return
		}
		// The metadata row changed during the unlocked read, so this may be a
		// torn read straddling a peer save or continue-as-new rather than
		// tampering. Re-check through the serialized cold-load path, which
		// retries torn reads and tombstones only if the failure is real.
		o.recordAudit(ctx, o.auditReload(ctx), start)

	case loadErr != nil:
		log.Debugf("Workflow actor '%s': audit could not load state: %s", o.actorID, loadErr)
		o.recordAudit(ctx, diag.IntegrityAuditError, start)

	case st == nil:
		// No state in the store while the cache holds some: either a purge
		// raced this sweep (benign, the actor is being deactivated) or every
		// row was deleted at the db level. Drop the cache so the next
		// operation observes the store truth.
		log.Warnf("Workflow actor '%s': background audit found no persisted state for cached workflow; dropping cache", o.actorID)
		o.invalidateCachedState()
		o.recordAudit(ctx, diag.IntegrityAuditDivergent, start)

	default:
		if o.auditDiverged(st, etagBefore) {
			// The store's signature chain verifies but the state does not
			// match the cache under our snapshot anchor. This covers benign
			// post-save races (a peer committed between a save and its ETag
			// refresh), replays of an older valid snapshot, and forged rows
			// that the chain does not cover (e.g. injected inbox events with
			// a rewritten metadata row). Adopt the store as truth through the
			// serialized cold-load path, whose inbox tamper scan and
			// propagated-history checks tombstone the forged cases.
			log.Warnf("Workflow actor '%s': background audit found persisted state diverging from cache; reloading from store", o.actorID)
			o.recordAudit(ctx, o.auditReload(ctx), start)
			return
		}
		o.recordAudit(ctx, diag.IntegrityAuditVerified, start)
	}
}

// auditReload drops the cache and reloads through the serialized cold-load
// path, which re-runs every load-time check (chain verification with
// torn-read retry, inbox tamper scan, persisted propagated-history
// verification) and tombstones when tampering is confirmed. Must be called
// with the actor lock held. Returns the audit result to record.
func (o *orchestrator) auditReload(ctx context.Context) string {
	o.invalidateCachedState()
	if _, _, err := o.loadInternalState(ctx); err != nil {
		log.Debugf("Workflow actor '%s': audit reload errored: %s", o.actorID, err)
		return diag.IntegrityAuditError
	}
	if o.state.HasTamperMarker() {
		log.Errorf("Workflow actor '%s': background audit reload confirmed state store tampering", o.actorID)
		return diag.IntegrityAuditTampered
	}
	return diag.IntegrityAuditDivergent
}

// auditDiverged reports whether the freshly loaded state differs from the
// cached state. RawHistory is not compared as the cache does not retain it
// after a save; RawSignatures is retained and anchors the chain, so
// byte-comparing it covers any history rewrite that also rewrote signatures.
func (o *orchestrator) auditDiverged(st *wfenginestate.State, etagBefore string) bool {
	if st.MetadataETag() == nil || *st.MetadataETag() != etagBefore {
		return true
	}
	if o.state.Generation != st.Generation ||
		len(o.state.History) != len(st.History) ||
		len(o.state.Inbox) != len(st.Inbox) ||
		len(o.state.RawSignatures) != len(st.RawSignatures) {
		return true
	}
	for i := range o.state.RawSignatures {
		if !bytes.Equal(o.state.RawSignatures[i], st.RawSignatures[i]) {
			return true
		}
	}
	return false
}

func (o *orchestrator) recordAudit(ctx context.Context, result string, start time.Time) {
	diag.DefaultWorkflowMonitoring.WorkflowIntegrityAudit(ctx, result, float64(time.Since(start).Milliseconds()))
}
