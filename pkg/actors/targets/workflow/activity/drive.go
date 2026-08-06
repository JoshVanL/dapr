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

package activity

import (
	"context"
	"time"

	"google.golang.org/protobuf/types/known/anypb"

	actorapi "github.com/dapr/dapr/pkg/actors/api"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/durabletask-go/api/protos"
)

const (
	// localDriveRetryInterval matches the cadence of the run-activity
	// reminder's constant FailurePolicy that the local drive replaces.
	localDriveRetryInterval = time.Second

	// localDriveMaxAttempts bounds local retries before escalating to the
	// durable run-activity reminder, which restores exactly the
	// retry-forever chain of the non-fast-path.
	localDriveMaxAttempts = 3

	// escalateTimeout bounds the durable-reminder create performed when a
	// local drive fails. The create is idempotent (overwrite-by-name) and
	// host-agnostic, and the workflow janitor remains the net if it also
	// fails.
	escalateTimeout = 30 * time.Second
)

// localDrive begins executing a certified activity invocation on this host in
// place of the elided run-activity reminder fire. It returns false when the
// drive cannot be armed (the factory is halting), in which case the caller
// MUST fall back to creating the durable reminder.
//
// The drive is detached: the arming Execute invocation holds the activity
// actor lock the execution's claim needs, and the execution can run for an
// arbitrary length of time while the orchestrator's dispatch must unblock
// immediately.
// It is scoped to the factory drive context, drained in HaltAll. Delivering
// the execution through router.CallReminder re-enters the normal
// InvokeReminder path, so locking, inflight dedup, error classification and
// deactivation are identical to a reminder fire.
func (a *activity) localDrive(invocation *protos.ActivityInvocation, dueTime time.Time, activityName *string) bool {
	f := a.factory

	f.driveLock.Lock()
	driveCtx := f.driveCtx
	if driveCtx.Err() != nil {
		f.driveLock.Unlock()
		return false
	}
	f.driveWG.Add(1)
	f.driveLock.Unlock()

	go f.driveActivity(driveCtx, a.actorID, invocation, dueTime, activityName)
	return true
}

// driveActivity runs one activity execution locally, retrying transient
// failures at the same cadence as the elided reminder's failure policy, and
// escalates to the durable run-activity reminder when the drive cannot
// complete here (repeated failure, or driveCtx cancellation on placement
// churn or shutdown, where a host-agnostic reminder create is exactly what
// is wanted). If the escalation also fails, the workflow janitor
// re-dispatches the unresolved task within one period.
func (f *factory) driveActivity(driveCtx context.Context, actorID string, invocation *protos.ActivityInvocation, dueTime time.Time, activityName *string) {
	defer f.driveWG.Done()

	anydata, err := anypb.New(invocation)
	if err != nil {
		// Unreachable for a just-decoded invocation; keep durability anyway.
		log.Errorf("Activity actor '%s': failed to marshal invocation for local drive: %v", actorID, err)
		f.escalateActivity(actorID, invocation, dueTime, activityName)
		return
	}

	// SkipRetries: this drive owns its recovery (bounded local retries, then
	// escalation to the durable reminder), so the router's blind 1s-backoff
	// retries would only delay it. SkipLock stays false: the execution claim
	// takes the activity actor lock like any locked reminder fire, and the
	// lock is released before the app roundtrip (see claim in execute.go).
	reminder := &actorapi.Reminder{
		Name:        activityReminderName,
		ActorType:   f.actorType,
		ActorID:     actorID,
		Data:        anydata,
		SkipRetries: true,
	}

	// No per-attempt deadline: activities run for arbitrary lengths and a
	// reminder-fired execution is equally unbounded. The bound is driveCtx.
	for attempt := 1; ; attempt++ {
		start := time.Now()
		err = f.router.CallReminder(driveCtx, reminder)
		elapsed := float64(time.Since(start)) / float64(time.Millisecond)

		if err == nil {
			diag.DefaultWorkflowMonitoring.WorkflowLocalActivity(context.Background(), diag.StatusSuccess)
			diag.DefaultWorkflowMonitoring.WorkflowLocalActivityDrive(context.Background(), diag.StatusSuccess, elapsed)
			return
		}
		diag.DefaultWorkflowMonitoring.WorkflowLocalActivityDrive(context.Background(), diag.StatusFailed, elapsed)

		if driveCtx.Err() != nil || attempt >= localDriveMaxAttempts {
			break
		}

		select {
		case <-driveCtx.Done():
		case <-time.After(localDriveRetryInterval):
			continue
		}
		break
	}

	log.Warnf("Activity actor '%s': local drive failed; escalating to a durable run-activity reminder: %v", actorID, err)
	diag.DefaultWorkflowMonitoring.WorkflowLocalActivity(context.Background(), diag.StatusFailed)
	f.escalateActivity(actorID, invocation, dueTime, activityName)
}

// escalateActivity creates the durable run-activity reminder after a failed
// local drive, restoring the non-fast-path recovery chain. It is detached
// from driveCtx (see driveActivity) and bounded by the factory root context
// plus escalateTimeout; escWG is not waited on the placement-churn path so
// HaltAll latency is unaffected.
func (f *factory) escalateActivity(actorID string, invocation *protos.ActivityInvocation, dueTime time.Time, activityName *string) {
	f.escLock.Lock()
	rootCtx := f.rootCtx
	if rootCtx.Err() != nil {
		f.escLock.Unlock()
		// Process shutdown: the workflow janitor (which survives in the
		// scheduler) re-dispatches on the next owner.
		diag.DefaultWorkflowMonitoring.WorkflowLocalActivity(context.Background(), diag.StatusEscalateSkipped)
		return
	}
	f.escWG.Add(1)
	f.escLock.Unlock()

	go func() {
		defer f.escWG.Done()

		ctx, cancel := context.WithTimeout(rootCtx, escalateTimeout)
		defer cancel()

		if err := f.createActivityReminder(ctx, actorID, invocation, dueTime, activityName); err != nil {
			log.Warnf("Activity actor '%s': failed to escalate to a durable run-activity reminder; the workflow janitor re-dispatches within one period: %v", actorID, err)
			diag.DefaultWorkflowMonitoring.WorkflowLocalActivity(context.Background(), diag.StatusEscalateFailed)
			return
		}
		diag.DefaultWorkflowMonitoring.WorkflowLocalActivity(context.Background(), diag.StatusEscalated)
	}()
}
