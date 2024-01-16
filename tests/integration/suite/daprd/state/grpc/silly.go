/*
Copyright 2023 The Dapr Authors
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

package grpc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(silly))
}

type silly struct {
	daprd *procdaprd.Daprd
}

func (b *silly) Setup(t *testing.T) []framework.Option {
	b.daprd = procdaprd.New(t, procdaprd.WithInMemoryActorStateStore("mystore"))

	return []framework.Option{
		framework.WithProcesses(b.daprd),
	}
}

func (b *silly) Run(t *testing.T, ctx context.Context) {
	b.daprd.WaitUntilRunning(t, ctx)

	client := b.daprd.GRPCClient(t, ctx)
	_, err := client.GetState(ctx, &rtv1.GetStateRequest{
		StoreName: "mystore",
		Key:       "key1",
	})
	require.Error(t, err)
}
