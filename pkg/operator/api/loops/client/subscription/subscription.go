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

package subscription

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	subapi "github.com/dapr/dapr/pkg/apis/subscriptions/v2alpha1"
	"github.com/dapr/dapr/pkg/operator/api/loops"
	"github.com/dapr/dapr/pkg/operator/api/loops/stream"
	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/kit/events/loop"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.operator.api.loops.client.subscription")

// LoopFactoryCache is the loop factory for subscription client loops.
var LoopFactoryCache = loop.New[loops.EventClient](1024)

var clientPool = sync.Pool{New: func() any {
	return new(subscriptionClient)
}}

// Options configures the subscription client loop.
type Options struct {
	// Stream is the gRPC server stream to send updates to.
	Stream operatorv1pb.Operator_SubscriptionUpdateServer
	// Namespace is the namespace to filter subscription updates.
	Namespace string
	// PodName is the name of the pod connecting.
	PodName string
}

// Client is a subscription client that manages a loop for processing
// subscription updates and sending them to a gRPC stream.
type Client struct {
	c *subscriptionClient
}

type subscriptionClient struct {
	namespace string
	podName   string

	loop       loop.Interface[loops.EventClient]
	streamLoop loop.Interface[loops.EventStream]

	wg sync.WaitGroup
}

// New creates a new subscription client that receives subscription
// updates and sends them to the gRPC stream.
func New(ctx context.Context, opts Options) *Client {
	c := clientPool.Get().(*subscriptionClient)

	c.namespace = opts.Namespace
	c.podName = opts.PodName

	c.loop = LoopFactoryCache.NewLoop(c)

	c.streamLoop = stream.New(stream.Options[*operatorv1pb.SubscriptionUpdateEvent]{
		Stream: opts.Stream,
	})

	return &Client{c: c}
}

// Run starts the client loop and blocks until context is done.
func (cl *Client) Run(ctx context.Context) error {
	c := cl.c

	// Wait for all goroutines when Run exits
	defer c.wg.Wait()

	// Start stream loop
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		if err := c.streamLoop.Run(ctx); err != nil {
			log.Errorf("Stream loop ended with error: %s", err)
		}
	}()

	// Start main loop in goroutine
	loopErrCh := make(chan error, 1)
	go func() {
		loopErrCh <- c.loop.Run(ctx)
	}()

	// Wait for either context cancellation or loop error
	var err error
	select {
	case <-ctx.Done():
		err = ctx.Err()
	case err = <-loopErrCh:
		// Loop exited unexpectedly
	}

	// Close the loop - this will trigger handleShutdown
	c.loop.Close(&loops.Shutdown{Error: err})

	return err
}

// Loop returns the underlying loop interface for enqueuing events.
func (cl *Client) Loop() loop.Interface[loops.EventClient] {
	return cl.c.loop
}

// CacheLoop returns the client loop to the pool for reuse.
func (cl *Client) CacheLoop() {
	LoopFactoryCache.CacheLoop(cl.c.loop)
}

// Close closes the client loop, causing Run to return.
func (cl *Client) Close() {
	cl.c.loop.Close(&loops.Shutdown{})
}

// Enqueue enqueues a subscription update event to the client loop.
// This is called by the apiServer when OnSubscriptionUpdated is invoked.
func Enqueue(cl *Client, sub *subapi.Subscription, eventType operatorv1pb.ResourceEventType) {
	cl.c.loop.Enqueue(&loops.SubscriptionUpdate{
		Subscription: sub,
		EventType:    eventType,
	})
}

func (c *subscriptionClient) Handle(ctx context.Context, event loops.EventClient) error {
	switch e := event.(type) {
	case *loops.SubscriptionUpdate:
		c.handleSubscriptionUpdate(ctx, e)
	case *loops.Shutdown:
		c.handleShutdown(e)
	default:
		panic(fmt.Sprintf("unknown subscription client event type: %T", e))
	}

	return nil
}

func (c *subscriptionClient) handleSubscriptionUpdate(ctx context.Context, e *loops.SubscriptionUpdate) {
	sub := e.Subscription
	if sub.Namespace != c.namespace {
		return
	}

	b, err := json.Marshal(sub)
	if err != nil {
		log.Warnf("error serializing subscription %s for pod %s/%s: %s", sub.GetName(), c.namespace, c.podName, err)
		return
	}

	c.streamLoop.Enqueue(&loops.StreamSend[*operatorv1pb.SubscriptionUpdateEvent]{
		Message: &operatorv1pb.SubscriptionUpdateEvent{
			Subscription: b,
			Type:         e.EventType,
		},
	})

	log.Debugf("updated sidecar with subscription %s %s to pod %s/%s", e.EventType.String(), sub.GetName(), c.namespace, c.podName)
}

func (c *subscriptionClient) handleShutdown(e *loops.Shutdown) {
	c.streamLoop.Close(&loops.Shutdown{Error: e.Error})
	stream.LoopFactory.CacheLoop(c.streamLoop)
}
