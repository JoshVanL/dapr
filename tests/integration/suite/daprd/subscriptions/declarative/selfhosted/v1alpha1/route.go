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

package v1alpha1

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/subscriber"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(route))
}

type route struct {
	daprd *daprd.Daprd
	sub   *subscriber.Subscriber
}

func (r *route) Setup(t *testing.T) []framework.Option {
	r.sub = subscriber.New(t)

	r.daprd = daprd.New(t,
		daprd.WithAppPort(r.sub.Port(t)),
		daprd.WithAppProtocol("grpc"),
		daprd.WithResourceFiles(`apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mypub
spec:
  type: pubsub.in-memory
  version: v1
---
apiVersion: dapr.io/v1alpha1
kind: Subscription
metadata:
  name: mysub1
spec:
  pubsubname: mypub
  topic: a
  route: /a/b/c/d
---
apiVersion: dapr.io/v1alpha1
kind: Subscription
metadata:
  name: mysub2
spec:
  pubsubname: mypub
  topic: a
  route: /a
---
apiVersion: dapr.io/v1alpha1
kind: Subscription
metadata:
  name: mysub3
spec:
  pubsubname: mypub
  topic: b
  route: /a
---
apiVersion: dapr.io/v1alpha1
kind: Subscription
metadata:
  name: mysub4
spec:
  pubsubname: mypub
  topic: b
  route: /a/b/c/d
`))

	return []framework.Option{
		framework.WithProcesses(r.sub, r.daprd),
	}
}

func (r *route) Run(t *testing.T, ctx context.Context) {
	r.daprd.WaitUntilRunning(t, ctx)
	client := r.daprd.GRPCClient(t, ctx)

	_, err := client.PublishEvent(ctx, &rtv1.PublishEventRequest{
		PubsubName: "mypub",
		Topic:      "a",
	})
	require.NoError(t, err)
	resp := r.sub.Receive(t, ctx)
	assert.Equal(t, "/a/b/c/d", resp.GetPath())
	assert.Empty(t, resp.GetData())

	_, err = client.PublishEvent(ctx, &rtv1.PublishEventRequest{
		PubsubName: "mypub",
		Topic:      "b",
	})
	require.NoError(t, err)
	resp = r.sub.Receive(t, ctx)
	assert.Equal(t, "/a", resp.GetPath())
	assert.Empty(t, resp.GetData())
}
