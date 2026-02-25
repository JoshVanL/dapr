/*
Copyright 2026 The Dapr Authors
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

package loops

import (
	componentsapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	httpendpointsapi "github.com/dapr/dapr/pkg/apis/httpEndpoint/v1alpha1"
	subapi "github.com/dapr/dapr/pkg/apis/subscriptions/v2alpha1"
	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"
)

type clientbase struct{}

func (*clientbase) isEventClient() {}

// EventClient is the marker interface for events handled by the client loop.
type EventClient interface{ isEventClient() }

type streambase struct{}

func (*streambase) isEventStream() {}

// EventStream is the marker interface for events handled by the stream loop.
type EventStream interface{ isEventStream() }

// ComponentUpdate is an event for a component update from the informer.
type ComponentUpdate struct {
	*clientbase
	Component *componentsapi.Component
	EventType operatorv1pb.ResourceEventType
}

// SubscriptionUpdate is an event for a subscription update from the callback.
type SubscriptionUpdate struct {
	*clientbase
	Subscription *subapi.Subscription
	EventType    operatorv1pb.ResourceEventType
}

// HTTPEndpointUpdate is an event for an HTTP endpoint update from the callback.
type HTTPEndpointUpdate struct {
	*clientbase
	Endpoint *httpendpointsapi.HTTPEndpoint
}

// StreamSend is an event to send a message over the gRPC stream.
type StreamSend[T any] struct {
	*streambase
	Message T
}

// Shutdown signals graceful shutdown.
type Shutdown struct {
	*clientbase
	*streambase
	Error error
}
