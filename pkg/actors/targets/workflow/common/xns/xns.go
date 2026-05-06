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

// Package xns hosts the shared cross-namespace bridge primitives used by
// both the orchestrator and activity actors. Concrete implementations of
// the Dispatcher live in pkg/runtime/wfengine/xns; this package only
// declares the contract and the deterministic key helper.
package xns

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strconv"

	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
)

// Hop identifies which leg of a cross-namespace exchange a deterministic
// key is keyed to. Dispatch = caller → target, Result = target → caller.
type Hop string

const (
	HopDispatch Hop = "dispatch"
	HopResult   Hop = "result"
)

// Dispatcher performs the sidecar-to-sidecar service-invocation portion of
// a cross-namespace workflow or activity call. It is split out from the
// orchestrator/activity actors so they don't depend on the gRPC manager /
// name resolver, which keeps unit tests mockable and confines the
// cross-ns wiring to a single assembly point.
type Dispatcher interface {
	// DispatchWorkflow sends a CrossNSDispatchRequest to the target sidecar
	// addressed as targetAppID in targetNamespace. Returns the gRPC status
	// error from the target so callers can special-case PermissionDenied /
	// Unimplemented / AlreadyExists without unwrapping.
	DispatchWorkflow(ctx context.Context, targetNamespace, targetAppID string, req *internalv1pb.CrossNSDispatchRequest) error

	// DeliverResult ships a CrossNSResultRequest back to the parent
	// orchestrator's sidecar.
	DeliverResult(ctx context.Context, parentNamespace, parentAppID string, req *internalv1pb.CrossNSResultRequest) error
}

// DeterministicKey derives an idempotency key for a cross-namespace hop.
// Including both parent and child executionIds ensures that a
// terminate+purge+rerun of the parent with the same instanceID produces a
// distinct key, preventing stale target-side reminders from colliding with
// the fresh run and preventing stale results from delivering into the new
// run's inbox. NUL byte separators between fields prevent
// boundary-collision attacks.
func DeterministicKey(sourceNs, sourceAppID, parentOrchID, parentExecID, childInstanceID, childExecID string, taskID int32, hop Hop) string {
	h := sha256.New()
	for _, part := range []string{
		sourceNs, sourceAppID,
		parentOrchID, parentExecID,
		childInstanceID, childExecID,
		strconv.FormatInt(int64(taskID), 10),
		string(hop),
	} {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}
