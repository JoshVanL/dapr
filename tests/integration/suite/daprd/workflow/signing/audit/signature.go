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

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	fworkflow "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/backend"
)

func init() {
	suite.Register(new(signature))
}

// signature verifies that a signature row tampered at the db level is
// detected by the background integrity audit while the orchestrator actor
// stays resident, without a daprd restart or any workflow activity.
type signature struct {
	workflow *workflow.Workflow
}

func (s *signature) Setup(t *testing.T) []framework.Option {
	s.workflow = workflow.New(t,
		workflow.WithMTLS(t),
		workflow.WithSigningAuditInterval("1s"),
	)

	return []framework.Option{
		framework.WithProcesses(s.workflow),
	}
}

func (s *signature) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)

	client := s.workflow.WorkflowClient(t, ctx)
	id := fworkflow.StartParkedWorkflow(t, ctx, client, fworkflow.ParkedRegistry("audit-signature"), "audit-signature")

	// Corrupt the signature bytes of the root signature row at the db level.
	sigKey, raw := s.workflow.DB().FirstStateValue(t, ctx, id, "signature")

	var sig backend.HistorySignature
	require.NoError(t, proto.Unmarshal(raw, &sig))
	require.NotEmpty(t, sig.GetSignature())

	sig.Signature[0] ^= 0xff

	updated, err := proto.Marshal(&sig)
	require.NoError(t, err)

	s.workflow.DB().WriteStateValue(t, ctx, sigKey, updated)

	// The background audit alone must detect the broken chain within its
	// interval and tombstone the workflow.
	fworkflow.WaitForTampered(t, ctx, client, id, time.Second*20)
}
