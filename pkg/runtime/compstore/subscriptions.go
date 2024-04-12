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

package compstore

import (
	"fmt"

	subapi "github.com/dapr/dapr/pkg/apis/subscriptions/v2alpha1"
	rtv1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	rtpubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
	"github.com/dapr/kit/logger"
)

type keyedSubscriptions struct {
	streamKeys      []string
	streams         []*rtpubsub.Subscription
	declarativeObjs []*subapi.Subscription
	declaratives    []*rtpubsub.Subscription
	programatic     []*rtpubsub.Subscription
}

type TopicRoutes map[string]TopicRouteElem

type TopicRouteElem struct {
	Metadata        map[string]string
	Rules           []*rtpubsub.Rule
	DeadLetterTopic string
	BulkSubscribe   *rtpubsub.BulkSubscribe
}

func (c *ComponentStore) SetTopicRoutes(topicRoutes map[string]TopicRoutes) {
	c.lock.Lock()
	defer c.lock.Unlock()
	c.topicRoutes = topicRoutes
}

func (c *ComponentStore) DeleteTopicRoute(name string) {
	c.lock.Lock()
	defer c.lock.Unlock()
	delete(c.topicRoutes, name)
}

func (c *ComponentStore) GetTopicRoutes() map[string]TopicRoutes {
	c.lock.RLock()
	defer c.lock.RUnlock()
	return c.topicRoutes
}

func (c *ComponentStore) ListSubscriptions(log logger.Logger) []*rtpubsub.Subscription {
	c.lock.RLock()
	defer c.lock.RUnlock()

	subscriptions := make([]*rtpubsub.Subscription, len(c.subscriptions.streams)+len(c.subscriptions.programatic))
	copy(subscriptions, c.subscriptions.streams)
	copy(subscriptions[len(c.subscriptions.streams):], c.subscriptions.programatic)

	for _, sub := range c.subscriptions.declaratives {
		skip := false

		// don't register duplicate subscriptions
		for _, s := range subscriptions {
			if sub.PubsubName == s.PubsubName && sub.Topic == s.Topic {
				log.Warnf("two identical subscriptions found (sources: declarative, app endpoint). pubsubname: %s, topic: %s",
					s.PubsubName, s.Topic)
				skip = true
				break
			}
		}

		if !skip {
			subscriptions = append(subscriptions, sub)
		}
	}

	return subscriptions
}

func (c *ComponentStore) SetProgramaticSubscriptions(subs ...rtpubsub.Subscription) {
	c.lock.Lock()
	defer c.lock.Unlock()
	c.subscriptions.programatic = make([]*rtpubsub.Subscription, len(subs))
	for i := range subs {
		c.subscriptions.programatic[i] = &subs[i]
	}
}

func (c *ComponentStore) AddDeclarativeSubscription(objs ...*subapi.Subscription) error {
	c.lock.Lock()
	defer c.lock.Unlock()

	for i := range objs {
		obj := objs[i]

		for _, existing := range c.subscriptions.declarativeObjs {
			if existing.Name == obj.Name {
				return fmt.Errorf("subscription with name %s already exists", obj.Name)
			}
		}

		sub := rtpubsub.Subscription{
			PubsubName:      obj.Spec.Pubsubname,
			Topic:           obj.Spec.Topic,
			DeadLetterTopic: obj.Spec.DeadLetterTopic,
			Metadata:        obj.Spec.Metadata,
			Scopes:          obj.Scopes,
			BulkSubscribe: &rtpubsub.BulkSubscribe{
				Enabled:            obj.Spec.BulkSubscribe.Enabled,
				MaxMessagesCount:   obj.Spec.BulkSubscribe.MaxMessagesCount,
				MaxAwaitDurationMs: obj.Spec.BulkSubscribe.MaxAwaitDurationMs,
			},
		}
		for _, rule := range obj.Spec.Routes.Rules {
			erule, err := rtpubsub.CreateRoutingRule(rule.Match, rule.Path)
			if err != nil {
				return err
			}
			sub.Rules = append(sub.Rules, erule)
		}
		if len(obj.Spec.Routes.Default) > 0 {
			sub.Rules = append(sub.Rules, &rtpubsub.Rule{
				Path: obj.Spec.Routes.Default,
			})
		}

		c.subscriptions.declarativeObjs = append(c.subscriptions.declarativeObjs, obj)
		c.subscriptions.declaratives = append(c.subscriptions.declaratives, &sub)
	}

	return nil
}

func (c *ComponentStore) ListDeclarativeSubscriptions() []*subapi.Subscription {
	c.lock.RLock()
	defer c.lock.RUnlock()
	return c.subscriptions.declarativeObjs
}

func (c *ComponentStore) GetDeclarativeSubscription(name string) (*subapi.Subscription, bool) {
	c.lock.RLock()
	defer c.lock.RUnlock()
	for i, sub := range c.subscriptions.declarativeObjs {
		if sub.ObjectMeta.Name == name {
			return c.subscriptions.declarativeObjs[i], true
		}
	}

	return nil, false
}

func (c *ComponentStore) DeleteDeclaraiveSubscription(names ...string) {
	c.lock.Lock()
	defer c.lock.Unlock()

	for _, name := range names {
		for i, sub := range c.subscriptions.declarativeObjs {
			if sub.ObjectMeta.Name == name {
				c.subscriptions.declarativeObjs = append(c.subscriptions.declarativeObjs[:i], c.subscriptions.declarativeObjs[i+1:]...)
				c.subscriptions.declaratives = append(c.subscriptions.declaratives[:i], c.subscriptions.declaratives[i+1:]...)
				break
			}
		}
	}
}

func (c *ComponentStore) AddStreamSubscription(key string, req *rtv1pb.SubscribeTopicEventsInitialRequest) error {
	c.lock.Lock()
	defer c.lock.Unlock()

	for _, existing := range c.subscriptions.streamKeys {
		if existing == key {
			return fmt.Errorf("stream subscription with key %s already exists", key)
		}
	}

	sub := rtpubsub.Subscription{
		PubsubName:      req.GetPubsubName(),
		Topic:           req.GetTopic(),
		DeadLetterTopic: req.GetDeadLetterTopic(),
		Metadata:        req.GetMetadata(),
		Rules:           []*rtpubsub.Rule{{Path: "/"}},
	}

	c.subscriptions.streamKeys = append(c.subscriptions.streamKeys, key)
	c.subscriptions.streams = append(c.subscriptions.streams, &sub)

	return nil
}

func (c *ComponentStore) DeleteStreamSubscription(keys ...string) {
	c.lock.Lock()
	defer c.lock.Unlock()

	for _, key := range keys {
		for i, k := range c.subscriptions.streamKeys {
			if key == k {
				c.subscriptions.streamKeys = append(c.subscriptions.streamKeys[:i], c.subscriptions.streamKeys[i+1:]...)
				c.subscriptions.streams = append(c.subscriptions.streams[:i], c.subscriptions.streams[i+1:]...)
				break
			}
		}
	}
}
