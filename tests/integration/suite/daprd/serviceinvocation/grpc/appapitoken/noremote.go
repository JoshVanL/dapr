/*
Copyright 2024 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implieh.
See the License for the specific language governing permissions and
limitations under the License.
*/

package appapitoken

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/app"
	"github.com/dapr/dapr/tests/integration/suite"
	testpb "github.com/dapr/dapr/tests/integration/suite/daprd/serviceinvocation/grpc/proto"
)

func init() {
	suite.Register(new(noremote))
}

type noremote struct {
	daprd1 *daprd.Daprd
	daprd2 *daprd.Daprd
	ch     chan metadata.MD
}

func (n *noremote) Setup(t *testing.T) []framework.Option {
	fn, ch := newServer()
	n.ch = ch
	app := app.New(t, app.WithRegister(fn))

	n.daprd1 = daprd.New(t,
		daprd.WithAppProtocol("grpc"),
		daprd.WithAppAPIToken(t, "abc"),
		daprd.WithAppPort(app.Port(t)),
	)

	n.daprd2 = daprd.New(t,
		daprd.WithAppProtocol("grpc"),
		daprd.WithAppPort(app.Port(t)),
	)

	return []framework.Option{
		framework.WithProcesses(app, n.daprd1, n.daprd2),
	}
}

func (n *noremote) Run(t *testing.T, ctx context.Context) {
	n.daprd1.WaitUntilRunning(t, ctx)
	n.daprd2.WaitUntilRunning(t, ctx)

	client := testpb.NewTestServiceClient(n.daprd1.GRPCConn(t, ctx))
	ctx = metadata.AppendToOutgoingContext(ctx, "dapr-app-id", n.daprd2.AppID())
	_, err := client.Ping(ctx, new(testpb.PingRequest))
	require.NoError(t, err)

	select {
	case md := <-n.ch:
		require.Empty(t, md.Get("dapr-api-token"))
	case <-time.After(5 * time.Second):
		assert.Fail(t, "timed out waiting for metadata")
	}
}
