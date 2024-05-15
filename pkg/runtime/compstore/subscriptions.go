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
	subapi "github.com/dapr/dapr/pkg/apis/subscriptions/v2alpha1"
	rtpubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
)

//type TopicRoutes map[string]TopicRouteElem

//func (c *ComponentStore) SetTopicRoutes(topicRoutes map[string]TopicRoutes) {
//	c.lock.Lock()
//	defer c.lock.Unlock()
//	c.topicRoutes = topicRoutes
//}
//
//func (c *ComponentStore) DeleteTopicRoute(name string) {
//	c.lock.Lock()
//	defer c.lock.Unlock()
//	delete(c.topicRoutes, name)
//}
//
//func (c *ComponentStore) GetTopicRoutes() map[string]TopicRoutes {
//	c.lock.RLock()
//	defer c.lock.RUnlock()
//	return c.topicRoutes
//}

//type TopicRoute struct {
//	Metadata        map[string]string
//	Rules           []*rtpubsub.Rule
//	DeadLetterTopic string
//	BulkSubscribe   *rtpubsub.BulkSubscribe
//}
//
//type Subscription struct {
//	Sub   rtpubsub.Subscription
//	Route TopicRoute
//}

type DeclarativeSubscription struct {
	Comp         *subapi.Subscription
	Subscription rtpubsub.Subscription
}

type subscriptions struct {
	programatics []rtpubsub.Subscription
	declaratives map[string]*DeclarativeSubscription
	streams      map[string]*DeclarativeSubscription
}

func (c *ComponentStore) SetProgramaticSubscriptions(subs ...rtpubsub.Subscription) {
	c.lock.Lock()
	defer c.lock.Unlock()
	c.subscriptions.programatics = subs
}

func (c *ComponentStore) AddDeclarativeSubscription(comp *subapi.Subscription, sub rtpubsub.Subscription) {
	c.lock.Lock()
	defer c.lock.Unlock()
	c.subscriptions.declaratives[comp.Name] = &DeclarativeSubscription{
		Comp:         comp,
		Subscription: sub,
	}
}

func (c *ComponentStore) AddStreamSubscription(comp *subapi.Subscription, sub rtpubsub.Subscription) {
	c.lock.Lock()
	defer c.lock.Unlock()
	c.subscriptions.streams[comp.Name] = &DeclarativeSubscription{
		Comp:         comp,
		Subscription: sub,
	}
}

func (c *ComponentStore) DeleteDeclarativeSubscription(names ...string) {
	c.lock.Lock()
	defer c.lock.Unlock()
	for _, name := range names {
		delete(c.subscriptions.declaratives, name)
	}
}

func (c *ComponentStore) ListSubscriptions() []rtpubsub.Subscription {
	c.lock.RLock()
	defer c.lock.RUnlock()

	subs := make([]rtpubsub.Subscription, 0, len(c.subscriptions.programatics)+len(c.subscriptions.declaratives)+len(c.subscriptions.streams))
	for i := range c.subscriptions.programatics {
		subs = append(subs, c.subscriptions.programatics[i])
	}
	for i := range c.subscriptions.declaratives {
		subs = append(subs, c.subscriptions.declaratives[i].Subscription)
	}
	for i := range c.subscriptions.streams {
		subs = append(subs, c.subscriptions.streams[i].Subscription)
	}

	return subs
}

func (c *ComponentStore) ListSubscriptionsApp() []rtpubsub.Subscription {
	c.lock.RLock()
	defer c.lock.RUnlock()

	subs := make([]rtpubsub.Subscription, 0, len(c.subscriptions.programatics)+len(c.subscriptions.declaratives))
	for _, sub := range c.subscriptions.programatics {
		subs = append(subs, sub)
	}
	for _, sub := range c.subscriptions.declaratives {
		subs = append(subs, sub.Subscription)
	}

	return subs
}

func (c *ComponentStore) ListSubscriptionsByPubSub(name string) []rtpubsub.Subscription {
	c.lock.RLock()
	defer c.lock.RUnlock()

	var subs []rtpubsub.Subscription
	for _, sub := range c.subscriptions.programatics {
		if sub.PubsubName == name {
			subs = append(subs, sub)
		}
	}
	for _, sub := range c.subscriptions.declaratives {
		if sub.Subscription.PubsubName == name {
			subs = append(subs, sub.Subscription)
		}
	}

	return subs
}

func (c *ComponentStore) ListSubscriptionsStreamByPubSub(name string) []rtpubsub.Subscription {
	c.lock.RLock()
	defer c.lock.RUnlock()

	var subs []rtpubsub.Subscription
	for _, sub := range c.subscriptions.streams {
		if sub.Subscription.PubsubName == name {
			subs = append(subs, sub.Subscription)
		}
	}

	return subs
}

func (c *ComponentStore) GetDeclarativeSubscription(name string) (*subapi.Subscription, bool) {
	c.lock.RLock()
	defer c.lock.RUnlock()
	for i, sub := range c.subscriptions.declaratives {
		if sub.Comp.Name == name {
			return c.subscriptions.declaratives[i].Comp, true
		}
	}
	return nil, false
}

func (c *ComponentStore) ListDeclarativeSubscriptions() []subapi.Subscription {
	c.lock.RLock()
	defer c.lock.RUnlock()
	subs := make([]subapi.Subscription, 0, len(c.subscriptions.declaratives))
	for i := range c.subscriptions.declaratives {
		subs = append(subs, *c.subscriptions.declaratives[i].Comp)
	}
	return subs
}

//func (c *ComponentStore) SetSubscriptions(subscriptions []rtpubsub.Subscription) {
//	c.lock.Lock()
//	defer c.lock.Unlock()
//	c.subscriptions = subscriptions
//}
//
//
//func (c *ComponentStore) AddDeclarativeSubscription(subscriptions ...subapi.Subscription) error {
//	c.lock.Lock()
//	defer c.lock.Unlock()
//
//	for _, sub := range subscriptions {
//		for _, existing := range c.declarativeSubscriptions {
//			if existing.ObjectMeta.Name == sub.ObjectMeta.Name {
//				return fmt.Errorf("subscription with name %s already exists", sub.ObjectMeta.Name)
//			}
//		}
//	}
//
//	c.declarativeSubscriptions = append(c.declarativeSubscriptions, subscriptions...)
//
//	return nil
//}
//
//func (c *ComponentStore) ListDeclarativeSubscriptions() []subapi.Subscription {
//	c.lock.RLock()
//	defer c.lock.RUnlock()
//	return c.declarativeSubscriptions
//}
//
//func (c *ComponentStore) GetDeclarativeSubscription(name string) (subapi.Subscription, bool) {
//	c.lock.RLock()
//	defer c.lock.RUnlock()
//	for i, sub := range c.declarativeSubscriptions {
//		if sub.ObjectMeta.Name == name {
//			return c.declarativeSubscriptions[i], true
//		}
//	}
//	return subapi.Subscription{}, false
//}
//
//func (c *ComponentStore) DeleteDeclaraiveSubscription(names ...string) {
//	c.lock.Lock()
//	defer c.lock.Unlock()
//
//	for _, name := range names {
//		for i, sub := range c.declarativeSubscriptions {
//			if sub.ObjectMeta.Name == name {
//				c.declarativeSubscriptions = append(c.declarativeSubscriptions[:i], c.declarativeSubscriptions[i+1:]...)
//				break
//			}
//		}
//	}
//}
