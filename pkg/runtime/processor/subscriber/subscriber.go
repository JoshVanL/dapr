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

package subscriber

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"google.golang.org/grpc"

	"github.com/dapr/dapr/pkg/api/grpc/manager"
	"github.com/dapr/dapr/pkg/config"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/channels"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	rtpubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
	"github.com/dapr/dapr/pkg/runtime/subscription"
	"github.com/dapr/kit/logger"
)

type Options struct {
	AppID       string
	Namespace   string
	Resiliency  resiliency.Provider
	TracingSpec *config.TracingSpec
	IsHTTP      bool
	Channels    *channels.Channels
	GRPC        *manager.Manager
	CompStore   *compstore.ComponentStore
}

type Subscriber struct {
	appID       string
	namespace   string
	resiliency  resiliency.Provider
	tracingSpec *config.TracingSpec
	isHTTP      bool
	channels    *channels.Channels
	grpc        *manager.Manager
	compStore   *compstore.ComponentStore

	appSubs      map[string][]*subscription.Subscription
	streamSubs   map[string][]*subscription.Subscription
	appSubActive bool
	hasInitProg  bool
	lock         sync.RWMutex
	running      atomic.Bool
	closed       bool
}

var log = logger.NewLogger("dapr.runtime.processor.subscription")

func New(opts Options) *Subscriber {
	return &Subscriber{
		appID:       opts.AppID,
		namespace:   opts.Namespace,
		resiliency:  opts.Resiliency,
		tracingSpec: opts.TracingSpec,
		isHTTP:      opts.IsHTTP,
		channels:    opts.Channels,
		grpc:        opts.GRPC,
		compStore:   opts.CompStore,
		appSubs:     make(map[string][]*subscription.Subscription),
		streamSubs:  make(map[string][]*subscription.Subscription),
	}
}

func (s *Subscriber) Run(ctx context.Context) error {
	if !s.running.CompareAndSwap(false, true) {
		return errors.New("subscriber is already running")
	}

	<-ctx.Done()

	s.StopAllSubscriptionsForever()

	return nil
}

func (s *Subscriber) ReloadPubSub(name string) error {
	s.lock.Lock()
	defer s.lock.Unlock()

	ps, ok := s.compStore.GetPubSub(name)
	if !ok {
		return nil
	}

	for _, sub := range s.appSubs[name] {
		sub.Stop()
	}
	for _, sub := range s.streamSubs[name] {
		sub.Stop()
	}

	s.appSubs = make(map[string][]*subscription.Subscription)
	s.streamSubs = make(map[string][]*subscription.Subscription)

	if err := s.initProgramaticSubscriptions(context.TODO()); err != nil {
		return err
	}

	if s.closed {
		return nil
	}

	if s.appSubActive {
		var subs []*subscription.Subscription
		for _, sub := range s.compStore.ListSubscriptionsByPubSub(name) {
			ss, err := subscription.New(subscription.Options{
				AppID:      s.appID,
				Namespace:  s.namespace,
				PubSubName: name,
				Topic:      sub.Topic,
				IsHTTP:     s.isHTTP,
				PubSub:     &ps,
				Resiliency: s.resiliency,
				TraceSpec:  s.tracingSpec,
				Route:      sub,
				Channels:   s.channels,
				GRPC:       s.grpc,
			})
			if err != nil {
				return err
			}

			subs = append(subs, ss)
		}
		s.appSubs[name] = subs
	}

	var subs []*subscription.Subscription
	for _, sub := range s.compStore.ListSubscriptionsStreamByPubSub(name) {
		ss, err := subscription.New(subscription.Options{
			AppID:      s.appID,
			Namespace:  s.namespace,
			PubSubName: name,
			Topic:      sub.Topic,
			IsHTTP:     s.isHTTP,
			PubSub:     &ps,
			Resiliency: s.resiliency,
			TraceSpec:  s.tracingSpec,
			Route:      sub,
			Channels:   s.channels,
			GRPC:       s.grpc,
		})
		if err != nil {
			return err
		}

		subs = append(subs, ss)
	}
	s.streamSubs[name] = subs

	return nil
}

func (s *Subscriber) StopPubSub(name string) {
	s.lock.Lock()
	defer s.lock.Unlock()

	if s.appSubs == nil && s.streamSubs == nil {
		return
	}

	for _, sub := range s.appSubs[name] {
		sub.Stop()
	}
	for _, sub := range s.streamSubs[name] {
		sub.Stop()
	}

	s.appSubs[name] = nil
	s.streamSubs[name] = nil
}

func (s *Subscriber) StartAppSubscriptions() error {
	s.lock.Lock()
	defer s.lock.Unlock()

	if s.appSubActive || s.closed {
		return nil
	}

	if err := s.initProgramaticSubscriptions(context.TODO()); err != nil {
		return err
	}

	s.appSubActive = true

	s.appSubs = make(map[string][]*subscription.Subscription)
	for _, sub := range s.compStore.ListSubscriptionsApp() {
		ps, ok := s.compStore.GetPubSub(sub.PubsubName)
		if !ok {
			continue
		}

		ss, err := subscription.New(subscription.Options{
			AppID:      s.appID,
			Namespace:  s.namespace,
			PubSubName: sub.PubsubName,
			Topic:      sub.Topic,
			IsHTTP:     s.isHTTP,
			PubSub:     &ps,
			Resiliency: s.resiliency,
			TraceSpec:  s.tracingSpec,
			Route:      sub,
			Channels:   s.channels,
			GRPC:       s.grpc,
		})
		if err != nil {
			return err
		}

		s.appSubs[sub.PubsubName] = append(s.appSubs[sub.PubsubName], ss)
	}

	return nil
}

func (s *Subscriber) StopAppSubscriptions() {
	s.lock.Lock()
	defer s.lock.Unlock()

	if !s.appSubActive {
		return
	}

	s.appSubActive = false

	for _, psub := range s.appSubs {
		for _, sub := range psub {
			sub.Stop()
		}
	}

	s.appSubs = make(map[string][]*subscription.Subscription)
}

func (s *Subscriber) StopAllSubscriptionsForever() {
	s.lock.Lock()
	defer s.lock.Unlock()

	s.closed = true

	for _, psubs := range s.appSubs {
		for _, sub := range psubs {
			sub.Stop()
		}
	}
	for _, psubs := range s.streamSubs {
		for _, sub := range psubs {
			sub.Stop()
		}
	}

	s.appSubs = nil
	s.streamSubs = nil
}

func (s *Subscriber) InitProgramaticSubscriptions(ctx context.Context) error {
	s.lock.Lock()
	defer s.lock.Unlock()
	return s.initProgramaticSubscriptions(ctx)
}

func (s *Subscriber) initProgramaticSubscriptions(ctx context.Context) error {
	if s.hasInitProg {
		return nil
	}

	if len(s.compStore.ListPubSubs()) == 0 {
		return nil
	}

	appChannel := s.channels.AppChannel()
	if appChannel == nil {
		log.Warn("app channel not initialized, make sure -app-port is specified if pubsub subscription is required")
		return nil
	}

	s.hasInitProg = true

	var (
		subscriptions []rtpubsub.Subscription
		err           error
	)

	// handle app subscriptions
	if s.isHTTP {
		subscriptions, err = rtpubsub.GetSubscriptionsHTTP(ctx, appChannel, log, s.resiliency)
	} else {
		var conn grpc.ClientConnInterface
		conn, err = s.grpc.GetAppClient()
		if err != nil {
			return fmt.Errorf("error while getting app client: %w", err)
		}
		client := runtimev1pb.NewAppCallbackClient(conn)
		subscriptions, err = rtpubsub.GetSubscriptionsGRPC(ctx, client, log, s.resiliency)
	}
	if err != nil {
		return err
	}

	s.compStore.SetProgramaticSubscriptions(subscriptions...)

	return nil
}
