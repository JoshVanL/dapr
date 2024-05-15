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

package subscription

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/dapr/components-contrib/metadata"
	contribpubsub "github.com/dapr/components-contrib/pubsub"
	"github.com/dapr/dapr/pkg/api/grpc/manager"
	"github.com/dapr/dapr/pkg/config"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/channels"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	rterrors "github.com/dapr/dapr/pkg/runtime/errors"
	rtpubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
	"github.com/dapr/kit/logger"
)

type Options struct {
	AppID      string
	Namespace  string
	PubSubName string
	Topic      string
	IsHTTP     bool
	PubSub     *compstore.PubsubItem
	Resiliency resiliency.Provider
	TraceSpec  *config.TracingSpec
	Route      compstore.TopicRouteElem
	Channels   *channels.Channels
	GRPC       *manager.Manager
}

type Subscription struct {
	appID       string
	namespace   string
	pubsubName  string
	topic       string
	isHTTP      bool
	pubsub      *compstore.PubsubItem
	resiliency  resiliency.Provider
	route       compstore.TopicRouteElem
	tracingSpec *config.TracingSpec
	channels    *channels.Channels
	grpc        *manager.Manager

	cancel func()
}

var log = logger.NewLogger("dapr.runtime.processor.pubsub.subscription")

func New(opts Options) (*Subscription, error) {
	ctx, cancel := context.WithCancel(context.Background())

	s := &Subscription{
		appID:       opts.AppID,
		namespace:   opts.Namespace,
		pubsubName:  opts.PubSubName,
		topic:       opts.Topic,
		isHTTP:      opts.IsHTTP,
		pubsub:      opts.PubSub,
		resiliency:  opts.Resiliency,
		route:       opts.Route,
		tracingSpec: opts.TraceSpec,
		channels:    opts.Channels,
		grpc:        opts.GRPC,
		cancel:      cancel,
	}

	name := s.pubsubName
	route := s.route
	policyDef := s.resiliency.ComponentInboundPolicy(name, resiliency.Pubsub)
	routeMetadata := route.Metadata

	namespaced := s.pubsub.NamespaceScoped

	if route.BulkSubscribe != nil && route.BulkSubscribe.Enabled {
		err := s.bulkSubscribeTopic(ctx, policyDef)
		if err != nil {
			cancel()
			return nil, fmt.Errorf("failed to bulk subscribe to topic %s: %w", s.topic, err)
		}
		return s, nil
	}

	// TOOD: @joshvanl: move subsscribedTopic to struct
	subscribeTopic := s.topic
	if namespaced {
		subscribeTopic = s.namespace + s.topic
	}

	err := s.pubsub.Component.Subscribe(ctx, contribpubsub.SubscribeRequest{
		Topic:    subscribeTopic,
		Metadata: routeMetadata,
	}, func(ctx context.Context, msg *contribpubsub.NewMessage) error {
		if msg.Metadata == nil {
			msg.Metadata = make(map[string]string, 1)
		}

		msg.Metadata[rtpubsub.MetadataKeyPubSub] = name

		msgTopic := msg.Topic
		if s.pubsub.NamespaceScoped {
			msgTopic = strings.Replace(msgTopic, s.namespace, "", 1)
		}

		rawPayload, err := metadata.IsRawPayload(route.Metadata)
		if err != nil {
			log.Errorf("error deserializing pubsub metadata: %s", err)
			if route.DeadLetterTopic != "" {
				if dlqErr := s.sendToDeadLetter(ctx, name, msg, route.DeadLetterTopic); dlqErr == nil {
					// dlq has been configured and message is successfully sent to dlq.
					diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, name, strings.ToLower(string(contribpubsub.Drop)), "", msgTopic, 0)
					return nil
				}
			}
			diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, name, strings.ToLower(string(contribpubsub.Retry)), "", msgTopic, 0)
			return err
		}

		var cloudEvent map[string]interface{}
		data := msg.Data
		if rawPayload {
			cloudEvent = contribpubsub.FromRawPayload(msg.Data, msgTopic, name)
			data, err = json.Marshal(cloudEvent)
			if err != nil {
				log.Errorf("error serializing cloud event in pubsub %s and topic %s: %s", name, msgTopic, err)
				if route.DeadLetterTopic != "" {
					if dlqErr := s.sendToDeadLetter(ctx, name, msg, route.DeadLetterTopic); dlqErr == nil {
						// dlq has been configured and message is successfully sent to dlq.
						diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, name, strings.ToLower(string(contribpubsub.Drop)), "", msgTopic, 0)
						return nil
					}
				}
				diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, name, strings.ToLower(string(contribpubsub.Retry)), "", msgTopic, 0)
				return err
			}
		} else {
			err = json.Unmarshal(msg.Data, &cloudEvent)
			if err != nil {
				log.Errorf("error deserializing cloud event in pubsub %s and topic %s: %s", name, msgTopic, err)
				if route.DeadLetterTopic != "" {
					if dlqErr := s.sendToDeadLetter(ctx, name, msg, route.DeadLetterTopic); dlqErr == nil {
						// dlq has been configured and message is successfully sent to dlq.
						diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, name, strings.ToLower(string(contribpubsub.Drop)), "", msgTopic, 0)
						return nil
					}
				}
				diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, name, strings.ToLower(string(contribpubsub.Retry)), "", msgTopic, 0)
				return err
			}
		}

		if contribpubsub.HasExpired(cloudEvent) {
			log.Warnf("dropping expired pub/sub event %v as of %v", cloudEvent[contribpubsub.IDField], cloudEvent[contribpubsub.ExpirationField])
			diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, name, strings.ToLower(string(contribpubsub.Drop)), "", msgTopic, 0)

			if route.DeadLetterTopic != "" {
				_ = s.sendToDeadLetter(ctx, name, msg, route.DeadLetterTopic)
			}
			return nil
		}

		routePath, shouldProcess, err := findMatchingRoute(route.Rules, cloudEvent)
		if err != nil {
			log.Errorf("error finding matching route for event %v in pubsub %s and topic %s: %s", cloudEvent[contribpubsub.IDField], name, msgTopic, err)
			if route.DeadLetterTopic != "" {
				if dlqErr := s.sendToDeadLetter(ctx, name, msg, route.DeadLetterTopic); dlqErr == nil {
					// dlq has been configured and message is successfully sent to dlq.
					diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, name, strings.ToLower(string(contribpubsub.Drop)), "", msgTopic, 0)
					return nil
				}
			}
			diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, name, strings.ToLower(string(contribpubsub.Retry)), "", msgTopic, 0)
			return err
		}

		if !shouldProcess {
			// The event does not match any route specified so ignore it.
			log.Debugf("no matching route for event %v in pubsub %s and topic %s; skipping", cloudEvent[contribpubsub.IDField], name, msgTopic)
			diag.DefaultComponentMonitoring.PubsubIngressEvent(ctx, name, strings.ToLower(string(contribpubsub.Drop)), strings.ToLower(string(contribpubsub.Success)), msgTopic, 0)
			if route.DeadLetterTopic != "" {
				_ = s.sendToDeadLetter(ctx, name, msg, route.DeadLetterTopic)
			}
			return nil
		}

		sm := &rtpubsub.SubscribedMessage{
			CloudEvent: cloudEvent,
			Data:       data,
			Topic:      msgTopic,
			Metadata:   msg.Metadata,
			Path:       routePath,
			PubSub:     name,
		}
		policyRunner := resiliency.NewRunner[any](context.Background(), policyDef)
		_, err = policyRunner(func(ctx context.Context) (any, error) {
			var pErr error
			// TODO: @joshvanl:
			//	ok, pErr := s.streamer.Publish(ctx, sm)
			//	if !ok {
			if s.isHTTP {
				pErr = s.publishMessageHTTP(ctx, sm)
			} else {
				pErr = s.publishMessageGRPC(ctx, sm)
			}
			//}

			var rErr *rterrors.RetriableError
			if errors.As(pErr, &rErr) {
				log.Warnf("encountered a retriable error while publishing a subscribed message to topic %s, err: %v", msgTopic, rErr.Unwrap())
			} else if errors.Is(pErr, rtpubsub.ErrMessageDropped) {
				// send dropped message to dead letter queue if configured
				if route.DeadLetterTopic != "" {
					derr := s.sendToDeadLetter(ctx, name, msg, route.DeadLetterTopic)
					if derr != nil {
						log.Warnf("failed to send dropped message to dead letter queue for topic %s: %v", msgTopic, derr)
					}
				}
				return nil, nil
			} else if pErr != nil {
				log.Errorf("encountered a non-retriable error while publishing a subscribed message to topic %s, err: %v", msgTopic, pErr)
			}
			return nil, pErr
		})
		if err != nil && err != context.Canceled {
			// Sending msg to dead letter queue.
			// If no DLQ is configured, return error for backwards compatibility (component-level retry).
			if route.DeadLetterTopic == "" {
				return err
			}
			_ = s.sendToDeadLetter(ctx, name, msg, route.DeadLetterTopic)
			return nil
		}
		return err
	})
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to subscribe to topic %s: %w", s.topic, err)
	}

	return s, nil
}

func (s *Subscription) Stop() {
	s.cancel()
}

func (s *Subscription) sendToDeadLetter(ctx context.Context, name string, msg *contribpubsub.NewMessage, deadLetterTopic string) error {
	req := &contribpubsub.PublishRequest{
		Data:        msg.Data,
		PubsubName:  name,
		Topic:       deadLetterTopic,
		Metadata:    msg.Metadata,
		ContentType: msg.ContentType,
	}

	if err := s.publish(ctx, req); err != nil {
		log.Errorf("error sending message to dead letter, origin topic: %s dead letter topic %s err: %w", msg.Topic, deadLetterTopic, err)
		return err
	}

	return nil
}

// TODO: @joshvanl
// Publish is an adapter method for the runtime to pre-validate publish requests
// And then forward them to the Pub/Sub component.
// This method is used by the HTTP and gRPC APIs.
func (s *Subscription) publish(ctx context.Context, req *contribpubsub.PublishRequest) error {
	if allowed := s.isOperationAllowed(req.Topic); !allowed {
		return rtpubsub.NotAllowedError{Topic: req.Topic, ID: s.appID}
	}

	if s.pubsub.NamespaceScoped {
		req.Topic = s.namespace + req.Topic
	}

	policyRunner := resiliency.NewRunner[any](ctx,
		s.resiliency.ComponentOutboundPolicy(req.PubsubName, resiliency.Pubsub),
	)
	_, err := policyRunner(func(ctx context.Context) (any, error) {
		return nil, s.pubsub.Component.Publish(ctx, req)
	})
	return err
}

func (s *Subscription) BulkPublish(ctx context.Context, req *contribpubsub.BulkPublishRequest) (contribpubsub.BulkPublishResponse, error) {
	if allowed := s.isOperationAllowed(req.Topic); !allowed {
		return contribpubsub.BulkPublishResponse{}, rtpubsub.NotAllowedError{Topic: req.Topic, ID: s.appID}
	}

	policyDef := s.resiliency.ComponentOutboundPolicy(req.PubsubName, resiliency.Pubsub)

	if contribpubsub.FeatureBulkPublish.IsPresent(s.pubsub.Component.Features()) {
		return rtpubsub.ApplyBulkPublishResiliency(ctx, req, policyDef, s.pubsub.Component.(contribpubsub.BulkPublisher))
	}

	log.Debugf("pubsub %s does not implement the BulkPublish API; falling back to publishing messages individually", req.PubsubName)
	defaultBulkPublisher := rtpubsub.NewDefaultBulkPublisher(s.pubsub.Component)

	return rtpubsub.ApplyBulkPublishResiliency(ctx, req, policyDef, defaultBulkPublisher)
}

func (s *Subscription) isOperationAllowed(topic string) bool {
	var inAllowedTopics, inProtectedTopics bool

	// first check if allowedTopics contain it
	if len(s.pubsub.AllowedTopics) > 0 {
		for _, t := range s.pubsub.AllowedTopics {
			if t == topic {
				inAllowedTopics = true
				break
			}
		}
		if !inAllowedTopics {
			return false
		}
	}

	// check if topic is protected
	if len(s.pubsub.ProtectedTopics) > 0 {
		for _, t := range s.pubsub.ProtectedTopics {
			if t == topic {
				inProtectedTopics = true
				break
			}
		}
	}

	// if topic is protected then a scope must be applied
	if !inProtectedTopics && len(s.pubsub.ScopedPublishings) == 0 {
		return true
	}

	// check if a granular scope has been applied
	allowedScope := false
	for _, t := range s.pubsub.ScopedPublishings {
		if t == topic {
			allowedScope = true
			break
		}
	}

	return allowedScope
}

// findMatchingRoute selects the path based on routing rules. If there are
// no matching rules, the route-level path is used.
func findMatchingRoute(rules []*rtpubsub.Rule, cloudEvent interface{}) (path string, shouldProcess bool, err error) {
	hasRules := len(rules) > 0
	if hasRules {
		data := map[string]interface{}{
			"event": cloudEvent,
		}
		rule, err := matchRoutingRule(rules, data)
		if err != nil {
			return "", false, err
		}
		if rule != nil {
			return rule.Path, true, nil
		}
	}

	return "", false, nil
}

func matchRoutingRule(rules []*rtpubsub.Rule, data map[string]interface{}) (*rtpubsub.Rule, error) {
	for _, rule := range rules {
		if rule.Match == nil || len(rule.Match.String()) == 0 {
			return rule, nil
		}
		iResult, err := rule.Match.Eval(data)
		if err != nil {
			return nil, err
		}
		result, ok := iResult.(bool)
		if !ok {
			return nil, fmt.Errorf("the result of match expression %s was not a boolean", rule.Match)
		}

		if result {
			return rule, nil
		}
	}

	return nil, nil
}
