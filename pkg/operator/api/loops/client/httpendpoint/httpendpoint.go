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

package httpendpoint

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"sigs.k8s.io/controller-runtime/pkg/client"

	httpendpointsapi "github.com/dapr/dapr/pkg/apis/httpEndpoint/v1alpha1"
	"github.com/dapr/dapr/pkg/operator/api/loops"
	"github.com/dapr/dapr/pkg/operator/api/loops/stream"
	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/kit/events/loop"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.operator.api.loops.client.httpendpoint")

// LoopFactoryCache is the loop factory for HTTP endpoint client loops.
var LoopFactoryCache = loop.New[loops.EventClient](1024)

var clientPool = sync.Pool{New: func() any {
	return new(httpEndpointClient)
}}

// Options configures the HTTP endpoint client loop.
type Options struct {
	// Stream is the gRPC server stream to send updates to.
	Stream operatorv1pb.Operator_HTTPEndpointUpdateServer
	// Namespace is the namespace to filter HTTP endpoint updates.
	Namespace string
	// PodName is the name of the pod connecting.
	PodName string
	// KubeClient is the Kubernetes client for secret resolution.
	KubeClient client.Client
	// ProcessSecrets is a function to process HTTP endpoint secrets.
	ProcessSecrets func(ctx context.Context, endpoint *httpendpointsapi.HTTPEndpoint, namespace string, kubeClient client.Client) error
}

// Client is an HTTP endpoint client that manages a loop for processing
// HTTP endpoint updates and sending them to a gRPC stream.
type Client struct {
	c *httpEndpointClient
}

type httpEndpointClient struct {
	namespace      string
	podName        string
	kubeClient     client.Client
	processSecrets func(ctx context.Context, endpoint *httpendpointsapi.HTTPEndpoint, namespace string, kubeClient client.Client) error

	loop       loop.Interface[loops.EventClient]
	streamLoop loop.Interface[loops.EventStream]

	wg sync.WaitGroup
}

// New creates a new HTTP endpoint client that receives HTTP endpoint
// updates and sends them to the gRPC stream.
func New(ctx context.Context, opts Options) *Client {
	c := clientPool.Get().(*httpEndpointClient)

	c.namespace = opts.Namespace
	c.podName = opts.PodName
	c.kubeClient = opts.KubeClient
	c.processSecrets = opts.ProcessSecrets

	c.loop = LoopFactoryCache.NewLoop(c)

	c.streamLoop = stream.New(stream.Options[*operatorv1pb.HTTPEndpointUpdateEvent]{
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

// Enqueue enqueues an HTTP endpoint update event to the client loop.
// This is called by the apiServer when OnHTTPEndpointUpdated is invoked.
func Enqueue(cl *Client, endpoint *httpendpointsapi.HTTPEndpoint) {
	cl.c.loop.Enqueue(&loops.HTTPEndpointUpdate{
		Endpoint: endpoint,
	})
}

func (c *httpEndpointClient) Handle(ctx context.Context, event loops.EventClient) error {
	switch e := event.(type) {
	case *loops.HTTPEndpointUpdate:
		c.handleHTTPEndpointUpdate(ctx, e)
	case *loops.Shutdown:
		c.handleShutdown(e)
	default:
		panic(fmt.Sprintf("unknown HTTP endpoint client event type: %T", e))
	}

	return nil
}

func (c *httpEndpointClient) handleHTTPEndpointUpdate(ctx context.Context, e *loops.HTTPEndpointUpdate) {
	endpoint := e.Endpoint
	if endpoint.Namespace != c.namespace {
		return
	}

	if c.processSecrets != nil {
		if err := c.processSecrets(ctx, endpoint, c.namespace, c.kubeClient); err != nil {
			log.Warnf("error processing http endpoint %s secrets from pod %s/%s: %s", endpoint.Name, c.namespace, c.podName, err)
			return
		}
	}

	b, err := json.Marshal(endpoint)
	if err != nil {
		log.Warnf("error serializing http endpoint %s from pod %s/%s: %s", endpoint.GetName(), c.namespace, c.podName, err)
		return
	}

	c.streamLoop.Enqueue(&loops.StreamSend[*operatorv1pb.HTTPEndpointUpdateEvent]{
		Message: &operatorv1pb.HTTPEndpointUpdateEvent{
			HttpEndpoints: b,
		},
	})

	log.Infof("updated sidecar with http endpoint %s from pod %s/%s", endpoint.GetName(), c.namespace, c.podName)
}

func (c *httpEndpointClient) handleShutdown(e *loops.Shutdown) {
	c.streamLoop.Close(&loops.Shutdown{Error: e.Error})
	stream.LoopFactory.CacheLoop(c.streamLoop)
}
