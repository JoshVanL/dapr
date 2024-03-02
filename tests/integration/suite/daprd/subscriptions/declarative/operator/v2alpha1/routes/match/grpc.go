/*
Copyright 2024 The Dapr Authors
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

package match

import (
	"context"
	"testing"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	subapi "github.com/dapr/dapr/pkg/apis/subscriptions/v2alpha1"
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/subscriber"
	"github.com/dapr/dapr/tests/integration/framework/process/kubernetes"
	"github.com/dapr/dapr/tests/integration/framework/process/operator"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(grpc))
}

type grpc struct {
	daprd    *daprd.Daprd
	kubeapi  *kubernetes.Kubernetes
	operator *operator.Operator
	sub      *subscriber.Subscriber
}

func (g *grpc) Setup(t *testing.T) []framework.Option {
	g.sub = subscriber.New(t)
	sentry := sentry.New(t, sentry.WithTrustDomain("integration.test.dapr.io"))

	g.kubeapi = kubernetes.New(t,
		kubernetes.WithBaseOperatorAPI(t,
			spiffeid.RequireTrustDomainFromString("integration.test.dapr.io"),
			"default",
			sentry.Port(),
		),
		kubernetes.WithClusterDaprComponentList(t, &compapi.ComponentList{
			Items: []compapi.Component{{
				ObjectMeta: metav1.ObjectMeta{Name: "mypub", Namespace: "default"},
				Spec: compapi.ComponentSpec{
					Type: "pubsub.in-memory", Version: "v1",
				},
			}},
		}),
		kubernetes.WithClusterDaprSubscriptionListV2(t, &subapi.SubscriptionList{
			Items: []subapi.Subscription{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "type", Namespace: "default"},
					Spec: subapi.SubscriptionSpec{
						Pubsubname: "mypub",
						Topic:      "type",
						Routes: subapi.Routes{
							Default: "/aaa",
							Rules: []subapi.Rule{
								{Path: "/type", Match: `event.type == "com.dapr.event.sent"`},
								{Path: "/foo", Match: ""},
								{Path: "/bar", Match: `event.type == "com.dapr.event.recv"`},
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "order1", Namespace: "default"},
					Spec: subapi.SubscriptionSpec{
						Pubsubname: "mypub",
						Topic:      "order1",
						Routes: subapi.Routes{
							Default: "/aaa",
							Rules: []subapi.Rule{
								{Path: "/type", Match: `event.type == "com.dapr.event.sent"`},
								{Path: "/topic", Match: `event.topic == "order1"`},
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "order2", Namespace: "default"},
					Spec: subapi.SubscriptionSpec{
						Pubsubname: "mypub",
						Topic:      "order2",
						Routes: subapi.Routes{
							Default: "/aaa",
							Rules: []subapi.Rule{
								{Path: "/topic", Match: `event.topic == "order2"`},
								{Path: "/type", Match: `event.type == "com.dapr.event.sent"`},
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "order3", Namespace: "default"},
					Spec: subapi.SubscriptionSpec{
						Pubsubname: "mypub",
						Topic:      "order3",
						Routes: subapi.Routes{
							Default: "/aaa",
							Rules: []subapi.Rule{
								{Path: "/123", Match: `event.topic == "order3"`},
								{Path: "/456", Match: `event.topic == "order3"`},
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "order4", Namespace: "default"},
					Spec: subapi.SubscriptionSpec{
						Pubsubname: "mypub",
						Topic:      "order4",
						Routes: subapi.Routes{
							Rules: []subapi.Rule{
								{Path: "/123", Match: `event.topic == "order5"`},
								{Path: "/456", Match: `event.topic == "order6"`},
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "order7", Namespace: "default"},
					Spec: subapi.SubscriptionSpec{
						Pubsubname: "mypub",
						Topic:      "order7",
						Routes: subapi.Routes{
							Default: "/order7def",
							Rules: []subapi.Rule{
								{Path: "/order7rule", Match: ""},
							},
						},
					},
				},
			},
		}),
	)

	g.operator = operator.New(t,
		operator.WithNamespace("default"),
		operator.WithKubeconfigPath(g.kubeapi.KubeconfigPath(t)),
		operator.WithTrustAnchorsFile(sentry.TrustAnchorsFile(t)),
	)

	g.daprd = daprd.New(t,
		daprd.WithMode("kubernetes"),
		daprd.WithSentryAddress(sentry.Address()),
		daprd.WithControlPlaneAddress(g.operator.Address()),
		daprd.WithAppPort(g.sub.Port(t)),
		daprd.WithAppProtocol("grpc"),
		daprd.WithDisableK8sSecretStore(true),
		daprd.WithEnableMTLS(true),
		daprd.WithNamespace("default"),
		daprd.WithExecOptions(exec.WithEnvVars(t,
			"DAPR_TRUST_ANCHORS", string(sentry.CABundle().TrustAnchors),
		)),
	)

	return []framework.Option{
		framework.WithProcesses(sentry, g.sub, g.kubeapi, g.operator, g.daprd),
	}
}

func (g *grpc) Run(t *testing.T, ctx context.Context) {
	g.operator.WaitUntilRunning(t, ctx)
	g.daprd.WaitUntilRunning(t, ctx)

	client := g.daprd.GRPCClient(t, ctx)

	_, err := client.PublishEvent(ctx, &rtv1.PublishEventRequest{
		PubsubName: "mypub",
		Topic:      "type",
	})
	require.NoError(t, err)
	resp := g.sub.Receive(t, ctx)
	assert.Equal(t, "/type", resp.GetPath())

	_, err = client.PublishEvent(ctx, &rtv1.PublishEventRequest{
		PubsubName: "mypub",
		Topic:      "order1",
	})
	require.NoError(t, err)
	resp = g.sub.Receive(t, ctx)
	assert.Equal(t, "/type", resp.GetPath())

	_, err = client.PublishEvent(ctx, &rtv1.PublishEventRequest{
		PubsubName: "mypub",
		Topic:      "order2",
	})
	require.NoError(t, err)
	resp = g.sub.Receive(t, ctx)
	assert.Equal(t, "/topic", resp.GetPath())

	_, err = client.PublishEvent(ctx, &rtv1.PublishEventRequest{
		PubsubName: "mypub",
		Topic:      "order3",
	})
	require.NoError(t, err)
	resp = g.sub.Receive(t, ctx)
	assert.Equal(t, "/123", resp.GetPath())

	g.sub.ExpectPublishNoReceive(t, ctx, g.daprd, &rtv1.PublishEventRequest{
		PubsubName: "mypub",
		Topic:      "order4",
	})

	_, err = client.PublishEvent(ctx, &rtv1.PublishEventRequest{
		PubsubName: "mypub",
		Topic:      "order7",
	})
	require.NoError(t, err)
	resp = g.sub.Receive(t, ctx)
	assert.Equal(t, "/order7rule", resp.GetPath())
}
