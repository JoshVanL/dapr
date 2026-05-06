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

package actors

import (
	"testing"

	"github.com/stretchr/testify/assert"

	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
)

// TestXNSReceiver_RejectsUnknownOpKind exercises the dispatch table on
// the inbound side. The receiver wraps the local Actors backend; we
// don't need a fully wired backend here because the unsupported-op
// path returns before any backend method is called.
func TestXNSReceiver_RejectsUnknownOpKind(t *testing.T) {
	r := NewXNSReceiver(nil)

	_, err := r.Execute(t.Context(), &internalsv1pb.ForwardOpRequest{
		Operation: internalsv1pb.WorkflowOpKind_WORKFLOW_OP_UNSPECIFIED,
	})
	assert.ErrorContains(t, err, "unsupported op kind")
}
