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

package pubsub

import (
	"context"
	"strings"
	"sync"

	contribpubsub "github.com/dapr/components-contrib/pubsub"
	"github.com/dapr/dapr/pkg/api/grpc/manager"
	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	comppubsub "github.com/dapr/dapr/pkg/components/pubsub"
	"github.com/dapr/dapr/pkg/config"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/channels"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	rterrors "github.com/dapr/dapr/pkg/runtime/errors"
	"github.com/dapr/dapr/pkg/runtime/meta"
	"github.com/dapr/dapr/pkg/runtime/processor/pubsub/subscription"
	"github.com/dapr/dapr/pkg/scopes"
	"github.com/dapr/kit/logger"
)

type Options struct {
	AppID          string
	Namespace      string
	Resiliency     resiliency.Provider
	TraceSpec      *config.TracingSpec
	Channels       *channels.Channels
	GRPC           *manager.Manager
	Registry       *comppubsub.Registry
	Meta           *meta.Meta
	ComponentStore *compstore.ComponentStore
}

type pubsub struct {
	appID       string
	namespace   string
	resiliency  resiliency.Provider
	tracingSpec *config.TracingSpec
	channels    *channels.Channels
	grpc        *manager.Manager
	registry    *comppubsub.Registry
	meta        *meta.Meta
	compStore   *compstore.ComponentStore

	appSubs      map[string][]*subscription.Subscription
	streamSubs   map[string][]*subscription.Subscription
	appSubActive bool
	lock         sync.RWMutex
}

var log = logger.NewLogger("dapr.runtime.processor.pubsub")

func New(opts Options) *pubsub {
	return &pubsub{
		appID:       opts.AppID,
		namespace:   opts.Namespace,
		resiliency:  opts.Resiliency,
		tracingSpec: opts.TraceSpec,
		channels:    opts.Channels,
		grpc:        opts.GRPC,
		registry:    opts.Registry,
		meta:        opts.Meta,
		compStore:   opts.ComponentStore,

		appSubs:    make(map[string][]*subscription.Subscription),
		streamSubs: make(map[string][]*subscription.Subscription),
	}
}

func (p *pubsub) Init(ctx context.Context, comp compapi.Component) error {
	p.lock.Lock()
	defer p.lock.Unlock()

	for _, sub := range p.appSubs[comp.Name] {
		sub.Stop()
	}
	p.appSubs[comp.Name] = nil

	fName := comp.LogName()
	pubSub, err := p.registry.Create(comp.Spec.Type, comp.Spec.Version, fName)
	if err != nil {
		diag.DefaultMonitoring.ComponentInitFailed(comp.Spec.Type, "creation", comp.ObjectMeta.Name)
		return rterrors.NewInit(rterrors.CreateComponentFailure, fName, err)
	}

	baseMetadata, err := p.meta.ToBaseMetadata(comp)
	if err != nil {
		diag.DefaultMonitoring.ComponentInitFailed(comp.Spec.Type, "init", comp.ObjectMeta.Name)
		return rterrors.NewInit(rterrors.InitComponentFailure, fName, err)
	}

	properties := baseMetadata.Properties
	consumerID := strings.TrimSpace(properties["consumerID"])
	if consumerID == "" {
		consumerID = p.appID
	}
	properties["consumerID"] = consumerID

	err = pubSub.Init(ctx, contribpubsub.Metadata{Base: baseMetadata})
	if err != nil {
		diag.DefaultMonitoring.ComponentInitFailed(comp.Spec.Type, "init", comp.ObjectMeta.Name)
		return rterrors.NewInit(rterrors.InitComponentFailure, fName, err)
	}

	pubsubName := comp.ObjectMeta.Name

	p.compStore.AddPubSub(pubsubName, compstore.PubsubItem{
		Component:           pubSub,
		ScopedSubscriptions: scopes.GetScopedTopics(scopes.SubscriptionScopes, p.appID, properties),
		ScopedPublishings:   scopes.GetScopedTopics(scopes.PublishingScopes, p.appID, properties),
		AllowedTopics:       scopes.GetAllowedTopics(properties),
		ProtectedTopics:     scopes.GetProtectedTopics(properties),
		NamespaceScoped:     meta.ContainsNamespace(comp.Spec.Metadata),
	})
	diag.DefaultMonitoring.ComponentInitialized(comp.Spec.Type)

	// TODO: @joshvanl
	//if p.subscribing {
	//	return p.beginPubSub(ctx, pubsubName)
	//}

	return nil
}

func (p *pubsub) Close(comp compapi.Component) error {
	p.lock.Lock()
	defer p.lock.Unlock()

	ps, ok := p.compStore.GetPubSub(comp.Name)
	if !ok {
		return nil
	}

	defer p.compStore.DeletePubSub(comp.Name)

	// TODO: @joshvanl
	//for topic := range p.compStore.GetTopicRoutes()[comp.Name] {
	//	subKey := topicKey(comp.Name, topic)
	//	p.unsubscribeTopic(subKey)
	//	p.compStore.DeleteTopicRoute(subKey)
	//}

	if err := ps.Component.Close(); err != nil {
		return err
	}

	return nil
}

func (p *pubsub) StartSubscriptions() {
	p.lock.Lock()
	defer p.lock.Unlock()

	if p.appSubActive {
		return
	}

	p.appSubActive = true
}

func (p *pubsub) StopSubscriptions() {
	p.lock.Lock()
	defer p.lock.Unlock()

	p.appSubActive = false

	for _, subs := range p.appSubs {
		for _, sub := range subs {
			sub.Stop()
		}
	}
}
