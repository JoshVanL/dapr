/*
Copyright 2023 The Dapr Authors
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
	"errors"
	"fmt"

	"google.golang.org/grpc"

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	rtpubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
)

// StartSubscriptions starts the pubsub subscriptions
func (p *pubsub) StartSubscriptions(ctx context.Context) error {
	p.lock.Lock()
	defer p.lock.Unlock()
	// Clean any previous state
	p.stopSubscriptions()
	return p.startSubscriptions(ctx)
}

// StopSubscriptions to all topics and cleans the cached topics
func (p *pubsub) StopSubscriptions(forever bool) {
	p.lock.Lock()
	defer p.lock.Unlock()

	if forever {
		// Mark if Dapr has stopped subscribing forever.
		p.stopForever = true
	}

	p.subscribing = false

	p.stopSubscriptions()
}

func (p *pubsub) startSubscriptions(ctx context.Context) error {
	// If Dapr has stopped subscribing forever, return early.
	if p.stopForever {
		return nil
	}

	p.subscribing = true

	var errs []error
	for pubsubName := range p.compStore.ListPubSubs() {
		if err := p.beginPubSub(ctx, pubsubName); err != nil {
			errs = append(errs, fmt.Errorf("error occurred while beginning pubsub %s: %v", pubsubName, err))
		}
	}

	return errors.Join(errs...)
}

func (p *pubsub) stopSubscriptions() {
	p.subCacheWarm = false
	for subKey := range p.topicCancels {
		p.unsubscribeTopic(subKey)
		p.compStore.DeleteTopicRoute(subKey)
	}
}

// ReloadSubscriptions reloads subscribers if subscribing.
func (p *pubsub) ReloadSubscriptions(ctx context.Context) error {
	p.lock.Lock()
	defer p.lock.Unlock()
	isSubscribing := p.subscribing
	if !isSubscribing {
		return nil
	}
	p.stopSubscriptions()
	return p.startSubscriptions(ctx)
}

func (p *pubsub) beginPubSub(ctx context.Context, name string) error {
	topicRoutes, err := p.topicRoutes(ctx)
	if err != nil {
		return err
	}

	v, ok := topicRoutes[name]
	if !ok {
		return nil
	}

	var errs []error
	for topic, route := range v {
		err = p.subscribeTopic(name, topic, route)
		if err != nil {
			errs = append(errs, fmt.Errorf("error occurred while beginning pubsub for topic %s on component %s: %v", topic, name, err))
		}
	}

	return errors.Join(errs...)
}

// topicRoutes returns a map of topic routes for all pubsubs.
func (p *pubsub) topicRoutes(ctx context.Context) (map[string]compstore.TopicRoutes, error) {
	if p.subCacheWarm {
		return p.compStore.GetTopicRoutes(), nil
	}

	topicRoutes := make(map[string]compstore.TopicRoutes)

	if p.channels == nil || p.channels.AppChannel() == nil {
		log.Warn("app channel not initialized, make sure -app-port is specified if pubsub subscription is required")
		return topicRoutes, nil
	}

	subscriptions, err := p.loadSubscriptions(ctx)
	if err != nil {
		return nil, err
	}

	for _, s := range subscriptions {
		if topicRoutes[s.PubsubName] == nil {
			topicRoutes[s.PubsubName] = compstore.TopicRoutes{}
		}

		topicRoutes[s.PubsubName][s.Topic] = compstore.TopicRouteElem{
			Metadata:        s.Metadata,
			Rules:           s.Rules,
			DeadLetterTopic: s.DeadLetterTopic,
			BulkSubscribe:   s.BulkSubscribe,
		}
	}

	if len(topicRoutes) > 0 {
		for pubsubName, v := range topicRoutes {
			var topics string
			for topic := range v {
				if topics == "" {
					topics += topic
				} else {
					topics += " " + topic
				}
			}
			log.Infof("app is subscribed to the following topics: [%s] through pubsub=%s", topics, pubsubName)
		}
	}
	p.compStore.SetTopicRoutes(topicRoutes)
	p.subCacheWarm = true
	return topicRoutes, nil
}

func (p *pubsub) loadSubscriptions(ctx context.Context) ([]*rtpubsub.Subscription, error) {
	appChannel := p.channels.AppChannel()
	if appChannel == nil {
		log.Warn("app channel not initialized, make sure -app-port is specified if pubsub subscription is required")
		return nil, nil
	}

	var (
		programatic []rtpubsub.Subscription
		err         error
	)

	// handle app subscriptions
	if p.isHTTP {
		programatic, err = rtpubsub.GetSubscriptionsHTTP(ctx, appChannel, log, p.resiliency)
	} else {
		var conn grpc.ClientConnInterface
		conn, err = p.grpc.GetAppClient()
		if err != nil {
			return nil, fmt.Errorf("error while getting app client: %w", err)
		}
		client := runtimev1pb.NewAppCallbackClient(conn)
		programatic, err = rtpubsub.GetSubscriptionsGRPC(ctx, client, log, p.resiliency)
	}
	if err != nil {
		return nil, err
	}

	p.compStore.SetProgramaticSubscriptions(programatic...)

	return p.compStore.ListSubscriptions(log), nil
}
