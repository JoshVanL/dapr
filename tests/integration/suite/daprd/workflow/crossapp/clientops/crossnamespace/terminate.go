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

// Package crossnamespace covers cross-namespace client-initiated workflow
// operations. The bridge actor (dapr.internal.<ns>.<appID>.workflow.xns)
// handles the durable forwarding/receiving on each side; the actual
// network transport between sidecars is performed by the production
// XNSForwarder, which is plugged in at daprd startup once cross-namespace
// service invocation is configured.
//
// These tests run against subprocess daprds, so the in-process fake
// Forwarder cannot bridge across them: each daprd needs a real Forwarder
// dialing its peer over the cluster network. Until that production
// wiring lands, the tests skip with a clear message describing the gap.
package crossnamespace

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(terminate))
}

type terminate struct {
	workflow *workflow.Workflow
}

func (te *terminate) Setup(t *testing.T) []framework.Option {
	te.workflow = workflow.New(t,
		workflow.WithDaprds(2),
		workflow.WithDaprdOptions(0, daprd.WithNamespace("default")),
		workflow.WithDaprdOptions(1, daprd.WithNamespace("other")),
	)
	return []framework.Option{
		framework.WithProcesses(te.workflow),
	}
}

func (te *terminate) Run(t *testing.T, ctx context.Context) {
	t.Skip("cross-namespace client-side terminate requires a production XNSForwarder wired into both daprds; tracked as follow-up to the bridge implementation")

	te.workflow.WaitUntilRunning(t, ctx)

	te.workflow.Registry().AddWorkflowN("LongRunner", func(wctx *task.WorkflowContext) (any, error) {
		return nil, wctx.CreateTimer(time.Hour).Await(nil)
	})

	host := te.workflow.BackendClient(t, ctx)
	from := te.workflow.BackendClientN(t, ctx, 1)
	app0 := te.workflow.DaprN(0).AppID()

	id, err := host.ScheduleNewWorkflow(ctx, "LongRunner")
	require.NoError(t, err)
	_, err = host.WaitForWorkflowStart(ctx, id)
	require.NoError(t, err)

	// Cross-namespace terminate from app1 (ns=other) targeting app0 (ns=default).
	require.NoError(t, from.TerminateWorkflow(ctx, id,
		api.WithTerminateAppID(app0),
		api.WithTerminateAppNamespace("default"),
	))

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		meta, ferr := host.FetchWorkflowMetadata(ctx, id)
		if !assert.NoError(c, ferr) {
			return
		}
		assert.Equal(c, api.RUNTIME_STATUS_TERMINATED, meta.GetRuntimeStatus())
	}, time.Second*30, time.Millisecond*50)
}
