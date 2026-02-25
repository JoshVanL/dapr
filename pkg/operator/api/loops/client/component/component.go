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

package component

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"sigs.k8s.io/controller-runtime/pkg/client"

	componentsapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/dapr/pkg/operator/api/informer"
	"github.com/dapr/dapr/pkg/operator/api/loops"
	"github.com/dapr/dapr/pkg/operator/api/loops/stream"
	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/kit/events/loop"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.operator.api.loops.client.component")

// LoopFactoryCache is the loop factory for component client loops.
var LoopFactoryCache = loop.New[loops.EventClient](1024)

var clientPool = sync.Pool{New: func() any {
	return new(componentClient)
}}

// Options configures the component client loop.
type Options struct {
	// EventCh is the channel to receive component events from the informer.
	EventCh <-chan *informer.Event[componentsapi.Component]
	// CancelWatch is the function to cancel the informer watch.
	CancelWatch context.CancelFunc
	// Stream is the gRPC server stream to send updates to.
	Stream operatorv1pb.Operator_ComponentUpdateServer
	// Namespace is the namespace to filter component updates.
	Namespace string
	// PodName is the name of the pod connecting.
	PodName string
	// KubeClient is the Kubernetes client for secret resolution.
	KubeClient client.Client
	// ProcessSecrets is a function to process component secrets.
	ProcessSecrets func(ctx context.Context, component *componentsapi.Component, namespace string, kubeClient client.Client) error
}

// Client is a component client that manages a loop for processing component
// updates and sending them to a gRPC stream.
type Client struct {
	c *componentClient
}

type componentClient struct {
	eventCh        <-chan *informer.Event[componentsapi.Component]
	cancelWatch    context.CancelFunc
	namespace      string
	podName        string
	kubeClient     client.Client
	processSecrets func(ctx context.Context, component *componentsapi.Component, namespace string, kubeClient client.Client) error

	loop       loop.Interface[loops.EventClient]
	streamLoop loop.Interface[loops.EventStream]

	// closeCh is closed when the event channel closes, signaling the loop should exit
	closeCh chan struct{}

	wg sync.WaitGroup
}

// New creates a new component client that receives events from the provided
// channel and sends updates to the gRPC stream.
func New(ctx context.Context, opts Options) *Client {
	c := clientPool.Get().(*componentClient)

	c.eventCh = opts.EventCh
	c.cancelWatch = opts.CancelWatch
	c.namespace = opts.Namespace
	c.podName = opts.PodName
	c.kubeClient = opts.KubeClient
	c.processSecrets = opts.ProcessSecrets
	c.closeCh = make(chan struct{})

	c.loop = LoopFactoryCache.NewLoop(c)

	c.streamLoop = stream.New(stream.Options[*operatorv1pb.ComponentUpdateEvent]{
		Stream: opts.Stream,
	})

	return &Client{c: c}
}

// Run starts the client loop and blocks until context is done or the event
// channel closes. It returns any error from the loop.
func (cl *Client) Run(ctx context.Context) error {
	c := cl.c

	// Wait for all goroutines when Run exits
	defer c.wg.Wait()

	// Start informer watcher goroutine
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		c.watchEvents(ctx)
	}()

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

	// Wait for either context cancellation or event channel closure
	var err error
	select {
	case <-ctx.Done():
		err = ctx.Err()
	case <-c.closeCh:
		// Event channel closed
		err = nil
	case err = <-loopErrCh:
		// Loop exited unexpectedly
	}

	// Close the loop - this will trigger handleShutdown
	c.loop.Close(&loops.Shutdown{Error: err})

	return err
}

// CacheLoop returns the client loop to the pool for reuse.
func (cl *Client) CacheLoop() {
	LoopFactoryCache.CacheLoop(cl.c.loop)
}

func (c *componentClient) watchEvents(ctx context.Context) {
	defer c.cancelWatch()

	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-c.eventCh:
			if !ok {
				// Event channel closed, signal the loop to exit
				close(c.closeCh)
				return
			}
			c.loop.Enqueue(&loops.ComponentUpdate{
				Component: &event.Manifest,
				EventType: event.Type,
			})
		}
	}
}

func (c *componentClient) Handle(ctx context.Context, event loops.EventClient) error {
	switch e := event.(type) {
	case *loops.ComponentUpdate:
		c.handleComponentUpdate(ctx, e)
	case *loops.Shutdown:
		c.handleShutdown(e)
	default:
		panic(fmt.Sprintf("unknown component client event type: %T", e))
	}

	return nil
}

func (c *componentClient) handleComponentUpdate(ctx context.Context, e *loops.ComponentUpdate) {
	comp := e.Component
	if comp.Namespace != c.namespace {
		return
	}

	if c.processSecrets != nil {
		if err := c.processSecrets(ctx, comp, c.namespace, c.kubeClient); err != nil {
			log.Warnf("error processing component %s secrets from pod %s/%s: %s", comp.Name, c.namespace, c.podName, err)
			return
		}
	}

	b, err := json.Marshal(comp)
	if err != nil {
		log.Warnf("error serializing component %s (%s) from pod %s/%s: %s", comp.GetName(), comp.Spec.Type, c.namespace, c.podName, err)
		return
	}

	c.streamLoop.Enqueue(&loops.StreamSend[*operatorv1pb.ComponentUpdateEvent]{
		Message: &operatorv1pb.ComponentUpdateEvent{
			Component: b,
			Type:      e.EventType,
		},
	})

	log.Debugf("updated sidecar with component %s %s (%s) from pod %s/%s", e.EventType.String(), comp.GetName(), comp.Spec.Type, c.namespace, c.podName)
}

func (c *componentClient) handleShutdown(e *loops.Shutdown) {
	c.streamLoop.Close(&loops.Shutdown{Error: e.Error})
	stream.LoopFactory.CacheLoop(c.streamLoop)
	// Note: we don't Put(c) here because CacheLoop() is called by the caller
}
