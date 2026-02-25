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

package stream

import (
	"context"
	"fmt"
	"sync"

	"github.com/dapr/dapr/pkg/operator/api/loops"
	"github.com/dapr/kit/events/loop"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.operator.api.loops.stream")

// Sender is an interface for sending messages over a gRPC stream.
type Sender[T any] interface {
	Send(T) error
}

// Options configures the stream loop.
type Options[T any] struct {
	Stream Sender[T]
}

type stream[T any] struct {
	sender Sender[T]
	loop   loop.Interface[loops.EventStream]
}

// LoopFactory creates new stream loops with pooling.
var LoopFactory = loop.New[loops.EventStream](64)

var streamPool = sync.Pool{New: func() any {
	return &stream[any]{}
}}

// New creates a new stream loop for sending messages over gRPC.
func New[T any](opts Options[T]) loop.Interface[loops.EventStream] {
	s := &stream[T]{
		sender: opts.Stream,
	}
	s.loop = LoopFactory.NewLoop(s)
	return s.loop
}

func (s *stream[T]) Handle(ctx context.Context, event loops.EventStream) error {
	switch e := event.(type) {
	case *loops.StreamSend[T]:
		return s.handleSend(e)
	case *loops.Shutdown:
		s.handleShutdown(e)
	default:
		panic(fmt.Sprintf("unknown stream event type: %T", e))
	}

	return nil
}

func (s *stream[T]) handleSend(e *loops.StreamSend[T]) error {
	if err := s.sender.Send(e.Message); err != nil {
		log.Warnf("error sending message: %s", err)
		return err
	}
	return nil
}

func (s *stream[T]) handleShutdown(e *loops.Shutdown) {
	log.Debugf("stream loop shutdown: %v", e.Error)
}
