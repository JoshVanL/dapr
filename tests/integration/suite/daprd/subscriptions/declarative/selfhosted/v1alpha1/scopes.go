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
	"fmt"
	"os"
	"path/filepath"
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
	suite.Register(new(scopes))
}

type scopes struct {
	daprd1 *daprd.Daprd
	daprd2 *daprd.Daprd
	sub    *subscriber.Subscriber
}

func (s *scopes) Setup(t *testing.T) []framework.Option {
	s.sub = subscriber.New(t)

	resDir := t.TempDir()

	s.daprd1 = daprd.New(t,
		daprd.WithAppPort(s.sub.Port(t)),
		daprd.WithAppProtocol("grpc"),
		daprd.WithResourcesDir(resDir),
	)
	s.daprd2 = daprd.New(t,
		daprd.WithAppPort(s.sub.Port(t)),
		daprd.WithAppProtocol("grpc"),
		daprd.WithResourcesDir(resDir),
	)

	require.NoError(t, os.WriteFile(filepath.Join(resDir, "sub.yaml"),
		[]byte(fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
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
  name: sub1
spec:
  pubsubname: mypub
  topic: all
  route: /all
---
apiVersion: dapr.io/v1alpha1
kind: Subscription
metadata:
  name: sub2
spec:
  pubsubname: mypub
  topic: allempty
  route: /allempty
scopes: []
---
apiVersion: dapr.io/v1alpha1
kind: Subscription
metadata:
  name: sub3
spec:
  pubsubname: mypub
  topic: only1
  route: /only1
scopes:
- %[1]s
---
apiVersion: dapr.io/v1alpha1
kind: Subscription
metadata:
  name: sub4
spec:
  pubsubname: mypub
  topic: only2
  route: /only2
scopes:
- %[2]s
---
apiVersion: dapr.io/v1alpha1
kind: Subscription
metadata:
  name: sub5
spec:
  pubsubname: mypub
  topic: both
  route: /both
scopes:
- %[1]s
- %[2]s
`, s.daprd1.AppID(), s.daprd2.AppID())), 0o600))

	return []framework.Option{
		framework.WithProcesses(s.sub, s.daprd1, s.daprd2),
	}
}

func (s *scopes) Run(t *testing.T, ctx context.Context) {
	s.daprd1.WaitUntilRunning(t, ctx)
	s.daprd2.WaitUntilRunning(t, ctx)

	client1 := s.daprd1.GRPCClient(t, ctx)
	client2 := s.daprd2.GRPCClient(t, ctx)

	meta, err := client1.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
	require.NoError(t, err)
	assert.Equal(t, []*rtv1.PubsubSubscription{
		{PubsubName: "mypub", Topic: "all", Rules: &rtv1.PubsubSubscriptionRules{
			Rules: []*rtv1.PubsubSubscriptionRule{{Path: "/all"}},
		}},
		{PubsubName: "mypub", Topic: "allempty", Rules: &rtv1.PubsubSubscriptionRules{
			Rules: []*rtv1.PubsubSubscriptionRule{{Path: "/allempty"}},
		}},
		{PubsubName: "mypub", Topic: "only1", Rules: &rtv1.PubsubSubscriptionRules{
			Rules: []*rtv1.PubsubSubscriptionRule{{Path: "/only1"}},
		}},
		{PubsubName: "mypub", Topic: "both", Rules: &rtv1.PubsubSubscriptionRules{
			Rules: []*rtv1.PubsubSubscriptionRule{{Path: "/both"}},
		}},
	}, meta.GetSubscriptions())

	meta, err = client2.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
	require.NoError(t, err)
	assert.Equal(t, []*rtv1.PubsubSubscription{
		{PubsubName: "mypub", Topic: "all", Rules: &rtv1.PubsubSubscriptionRules{
			Rules: []*rtv1.PubsubSubscriptionRule{{Path: "/all"}},
		}},
		{PubsubName: "mypub", Topic: "allempty", Rules: &rtv1.PubsubSubscriptionRules{
			Rules: []*rtv1.PubsubSubscriptionRule{{Path: "/allempty"}},
		}},
		{PubsubName: "mypub", Topic: "only2", Rules: &rtv1.PubsubSubscriptionRules{
			Rules: []*rtv1.PubsubSubscriptionRule{{Path: "/only2"}},
		}},
		{PubsubName: "mypub", Topic: "both", Rules: &rtv1.PubsubSubscriptionRules{
			Rules: []*rtv1.PubsubSubscriptionRule{{Path: "/both"}},
		}},
	}, meta.GetSubscriptions())

	newReq := func(topic string) *rtv1.PublishEventRequest {
		return &rtv1.PublishEventRequest{PubsubName: "mypub", Topic: topic, Data: []byte(`{"status": "completed"}`)}
	}

	reqAll := newReq("all")
	reqEmpty := newReq("allempty")
	reqOnly1 := newReq("only1")
	reqOnly2 := newReq("only2")
	reqBoth := newReq("both")

	s.sub.ExpectPublishReceive(t, ctx, s.daprd1, reqAll)
	s.sub.ExpectPublishReceive(t, ctx, s.daprd1, reqEmpty)
	s.sub.ExpectPublishReceive(t, ctx, s.daprd1, reqOnly1)
	s.sub.ExpectPublishNoReceive(t, ctx, s.daprd1, reqOnly2)
	s.sub.ExpectPublishReceive(t, ctx, s.daprd1, reqBoth)

	s.sub.ExpectPublishReceive(t, ctx, s.daprd2, reqAll)
	s.sub.ExpectPublishReceive(t, ctx, s.daprd2, reqEmpty)
	s.sub.ExpectPublishNoReceive(t, ctx, s.daprd2, reqOnly1)
	s.sub.ExpectPublishReceive(t, ctx, s.daprd2, reqOnly2)
	s.sub.ExpectPublishReceive(t, ctx, s.daprd2, reqBoth)

	s.sub.EventChanLen(t, 0)
}
