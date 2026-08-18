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
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	fworkflow "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
)

func init() {
	suite.Register(new(inboxinject))
}

// inboxinject verifies the audit also covers forgeries the signature chain
// does not: an inbox event injected at the db level (with the metadata row
// rewritten to declare it) verifies against the chain, but diverges from the
// resident actor's cache. The audit adopts the store through the serialized
// cold-load path, whose inbox tamper scan detects the forged completion and
// tombstones the workflow. No restart and no client event drive detection.
type inboxinject struct {
	workflow *workflow.Workflow
}

func (i *inboxinject) Setup(t *testing.T) []framework.Option {
	i.workflow = workflow.New(t,
		workflow.WithMTLS(t),
		workflow.WithSigningAuditInterval("1s"),
	)

	return []framework.Option{
		framework.WithProcesses(i.workflow),
	}
}

func (i *inboxinject) Run(t *testing.T, ctx context.Context) {
	i.workflow.WaitUntilRunning(t, ctx)

	client := i.workflow.WorkflowClient(t, ctx)
	id := fworkflow.StartParkedWorkflow(t, ctx, client, fworkflow.ActivityParkedRegistry("audit-inbox"), "audit-inbox")

	// Wait until the activity completion is signed into history and the
	// workflow is parked on the external event.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, fworkflow.SignatureCount(t, ctx, i.workflow.DB(), id), 2)
	}, time.Second*10, time.Millisecond*100)

	// Inject a fake TaskCompleted event referencing a TaskScheduledId that
	// was never scheduled, and rewrite the metadata row to declare it.
	appID := i.workflow.Dapr().AppID()
	keyPrefix := appID + "||dapr.internal.default." + appID + ".workflow||" + id + "||"

	fakeEvt := &protos.HistoryEvent{
		EventId:   int32(-1),
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_TaskCompleted{
			TaskCompleted: &protos.TaskCompletedEvent{
				TaskScheduledId: int32(9999),
				Result:          wrapperspb.String(`"injected"`),
			},
		},
	}
	raw, err := proto.Marshal(fakeEvt)
	require.NoError(t, err)

	i.workflow.DB().WriteStateValue(t, ctx, fmt.Sprintf("%sinbox-%06d", keyPrefix, 0), raw)

	fworkflow.MutateMetadata(t, ctx, i.workflow.DB(), id, func(m *backend.BackendWorkflowStateMetadata) {
		m.InboxLength = 1
	})

	// The audit alone must detect the forged inbox entry and tombstone the
	// workflow.
	fworkflow.WaitForTampered(t, ctx, client, id, time.Second*20)
}
