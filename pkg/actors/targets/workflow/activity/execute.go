/*
Copyright 2025 The Dapr Authors
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
	"errors"
	"fmt"
	"strings"

	actorsapi "github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/activity/inflight"
	"github.com/dapr/dapr/pkg/messages"
	"github.com/dapr/durabletask-go/api/protos"
)

func (a *activity) executeActivity(ctx context.Context, name string, invocation *protos.ActivityInvocation, skipLock bool) error {
	taskEvent := invocation.GetHistoryEvent()
	activityName := ""
	if ts := taskEvent.GetTaskScheduled(); ts != nil {
		activityName = ts.GetName()
	} else {
		return fmt.Errorf("invalid activity task event: '%s'", taskEvent.String())
	}

	endIndex := strings.Index(a.actorID, "::")
	if endIndex < 0 {
		return fmt.Errorf("invalid activity actor ID: '%s'", a.actorID)
	}
	workflowID := a.actorID[0:endIndex]

	// Cryptographically verify any propagated history before letting the
	// activity see it. Activities are stateless workers with no
	// ext-sigcert table to absorb certs into, so this is a verify-or-
	// reject gate. The helper handles the disabled-signer case (logs a
	// warning if a signed payload arrives) and the nil-payload case
	// internally. On failure, abort activity execution: the caller (parent
	// workflow) gets a recoverable error and the activity never runs.
	if err := a.signing.VerifyPropagatedHistoryStateless(invocation.GetPropagatedHistory()); err != nil {
		return fmt.Errorf("activity '%s::%d' rejecting invocation: propagated history verification failed: %w", activityName, taskEvent.GetEventId(), err)
	}

	key := inflight.Key(a.actorID, taskEvent)
	call, owner, err := a.claim(ctx, key, skipLock)
	if err != nil {
		return err
	}
	if !owner {
		// A previous reminder for this activity scheduling is already in
		// flight (or just finished and its outcome is still cached). Wait
		// for its result and surface the same outcome so the scheduler's
		// retry can be acked without dispatching the activity to the SDK
		// again. The owner is responsible for posting the result to the
		// workflow actor.
		log.Debugf("Activity actor '%s': following in-flight execution of '%s'", a.actorID, name)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-call.Done():
			return call.Err()
		}
	}

	return a.runOwned(ctx, key, call, name, activityName, workflowID, taskEvent, invocation)
}

// claim acquires the inflight entry for key, taking the actor lock for the
// claim only unless the caller asked to skip it (scheduler-stream fires
// arrive with SkipLock set and already relied on the inflight entry alone).
// The lock MUST NOT extend past the claim: the segments that follow are the
// app roundtrip (arbitrary length) and the result delivery into the parent
// workflow (contends on the parent's turn lock), and holding the per-actor
// lock across either parks Execute dispatches mesh-wide behind slow parent
// turns. Neither segment needs the actor's serialization: the inflight entry
// dedups duplicate arrivals (they join as followers, locked or not), and a
// crash mid-execution is recovered by the parent janitor re-dispatching the
// unresolved TaskScheduled event.
func (a *activity) claim(ctx context.Context, key string, skipLock bool) (*inflight.Call, bool, error) {
	if !skipLock {
		unlock, err := a.lock.ContextLock(ctx)
		if err != nil {
			return nil, false, err
		}
		defer unlock()
	}

	call, owner := a.inflight.Acquire(key)
	return call, owner, nil
}

func (f *factory) actorNotReachable(ctx context.Context, wfActorType, workflowID string) bool {
	_, _, cancel, err := f.placement.LookupActor(ctx, &actorsapi.LookupActorRequest{
		ActorType: wfActorType,
		ActorID:   workflowID,
	})
	if cancel != nil {
		cancel(nil)
	}
	return errors.Is(err, messages.ErrActorNoAddress)
}
