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
	"time"

	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	actorapi "github.com/dapr/dapr/pkg/actors/api"
	diag "github.com/dapr/dapr/pkg/diagnostics"
)

// localWakeTimeout bounds a single detached local wake attempt, including the
// time it spends queued on the actor lock behind the arming invocation.
const localWakeTimeout = time.Minute

// maybeLocalWake eagerly drives a just-armed wake-up reminder on this host
// instead of waiting for the scheduler to fire it, cutting the scheduler
// trigger-delivery leg out of the workflow hot path. On a successful turn the
// scheduler backstop reminder is proactively deleted so it never fires.
//
// MUST be called only after BOTH the state save AND the durable reminder
// create succeeded: the scheduler entry is the crash backstop and has to
// exist before a local turn is allowed to delete it. The wake goroutine is
// detached (the arming invocation holds the actor lock the wake turn needs)
// and scoped to the factory's wake context, drained in HaltAll.
//
// No-op when the WorkflowsLocalWakeFastPath preview feature is off or the
// wake is scheduled in the future (delayed starts must keep their scheduler
// due time).
//
// Failure handling mirrors the in-memory timers: on any error the goroutine
// only logs, and the untouched durable backstop drives the turn through the
// scheduler as it does today.
//
// A benign race exists for EventRaised wake-ups: their reminder name hashes
// only the event name, so the proactive delete can race a second same-name
// raise re-arming the reminder. The second raise performs its own local wake
// (or its retry-forever create re-asserts the backstop), and the empty-inbox
// path reloads durable state before acking, so no event is ever lost.
func (o *orchestrator) maybeLocalWake(reminderName string, dueTime time.Time) {
	if !o.localWakeFastPath || dueTime.After(time.Now()) {
		return
	}

	// Serialize the spawn against HaltAll's cancel/recreate cycle: either
	// the Add happens before the cancel (and HaltAll waits for this wake),
	// or the context is already cancelled and the spawn is skipped.
	o.wakeLock.Lock()
	wakeCtx := o.wakeCtx
	if wakeCtx.Err() != nil {
		o.wakeLock.Unlock()
		return
	}
	o.wakeWG.Add(1)
	o.wakeLock.Unlock()

	actorType := o.actorTypeBuilder.Workflow(o.appID)
	actorID := o.actorID

	go func() {
		defer o.wakeWG.Done()

		ctx, cancel := context.WithTimeout(wakeCtx, localWakeTimeout)
		defer cancel()

		// Data is nil: wake-up reminders carry no payload; the turn reloads
		// the durable inbox. SkipLock is false, matching the scheduler
		// streamer for workflow actor types. The router resolves placement,
		// so if the actor migrated between arming and wake the turn is
		// delivered to the new owner host.
		err := o.router.CallReminder(ctx, &actorapi.Reminder{
			Name:      reminderName,
			ActorType: actorType,
			ActorID:   actorID,
		})
		if err != nil {
			log.Debugf("Workflow actor '%s': local wake '%s' failed; the scheduler backstop will drive it: %v", actorID, reminderName, err)
			diag.DefaultWorkflowMonitoring.WorkflowLocalWake(ctx, diag.StatusFailed)
			return
		}

		diag.DefaultWorkflowMonitoring.WorkflowLocalWake(ctx, diag.StatusSuccess)

		// The turn ran locally: delete the scheduler backstop so it never
		// fires. Losing this delete is safe: the backstop firing lands on
		// the empty-inbox path, which reloads durable state and acks clean.
		if derr := o.reminders.Delete(ctx, &actorapi.DeleteReminderRequest{
			Name:      reminderName,
			ActorType: actorType,
			ActorID:   actorID,
		}); derr != nil {
			if s, ok := grpcstatus.FromError(derr); !ok || s.Code() != codes.NotFound {
				log.Debugf("Workflow actor '%s': failed to delete backstop reminder '%s' after local wake (a spurious empty-inbox turn may fire): %v", actorID, reminderName, derr)
			}
		}
	}()
}
