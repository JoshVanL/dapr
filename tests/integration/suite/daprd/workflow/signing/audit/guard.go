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

package audit

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	wferrors "github.com/dapr/dapr/pkg/runtime/wfengine/state/errors"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	fworkflow "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/backend"
	dworkflow "github.com/dapr/durabletask-go/workflow"
)

func init() {
	suite.Register(new(guard))
}

// guard verifies the save-path chain-head guard: with the background audit
// effectively disabled (1h interval), tampering the chain-head signature row
// at the db level is still detected on the workflow's very next save. Every
// save re-upserts the chain head with its known row version, so the tampered
// row fails the transaction with an ETag mismatch, the cache is dropped, and
// the following cold load runs full chain verification and tombstones the
// workflow.
type guard struct {
	workflow *workflow.Workflow
}

func (g *guard) Setup(t *testing.T) []framework.Option {
	g.workflow = workflow.New(t,
		workflow.WithMTLS(t),
		workflow.WithSigningAuditInterval("1h"),
	)

	return []framework.Option{
		framework.WithProcesses(g.workflow),
	}
}

func (g *guard) Run(t *testing.T, ctx context.Context) {
	g.workflow.WaitUntilRunning(t, ctx)

	client := g.workflow.WorkflowClient(t, ctx)
	id := fworkflow.StartParkedWorkflow(t, ctx, client, fworkflow.ParkedRegistry("audit-guard"), "audit-guard")

	// Corrupt the chain-head signature row at the db level. The write also
	// changes the row's version, which is what the guard asserts against.
	sigKey, raw := g.workflow.DB().LastStateValue(t, ctx, id, "signature")

	var sig backend.HistorySignature
	require.NoError(t, proto.Unmarshal(raw, &sig))
	require.NotEmpty(t, sig.GetSignature())

	sig.Signature[0] ^= 0xff

	updated, err := proto.Marshal(&sig)
	require.NoError(t, err)

	g.workflow.DB().WriteStateValue(t, ctx, sigKey, updated)

	// Drive saves by raising the awaited event. The first save fails on the
	// guard's ETag mismatch and drops the cache; the retried activation cold
	// loads, fails chain verification, and tombstones the workflow. RaiseEvent
	// errors are expected along the way, so raise inside the retry loop.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		//nolint:errcheck
		client.RaiseEvent(ctx, id, "continue", dworkflow.WithEventPayload("real-event"))

		wmeta, werr := client.FetchWorkflowMetadata(ctx, id)
		assert.NoError(c, werr)
		if !assert.NotNil(c, wmeta) {
			return
		}
		if !assert.Equal(c, dworkflow.StatusFailed, wmeta.RuntimeStatus) {
			return
		}
		if !assert.NotNil(c, wmeta.FailureDetails) {
			return
		}
		assert.Equal(c, wferrors.ErrorTypeHistoryTampered, wmeta.FailureDetails.GetErrorType())
	}, time.Second*20, time.Millisecond*100)
}
