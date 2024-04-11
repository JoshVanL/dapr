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

package pubsub

import (
	"context"
	"sync"

	rtv1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
)

type streamconn struct {
	lock             sync.RWMutex
	s                *streamer
	publishResponses map[string]chan *rtv1pb.SubscribeTopicEventsResponse
	stream           rtv1pb.Dapr_SubscribeTopicEventsServer
}

func (s *streamconn) notifyPublishResponse(ctx context.Context, resp *rtv1pb.SubscribeTopicEventsResponse) {
	s.lock.RLock()
	ch, ok := s.publishResponses[resp.GetId()]
	s.lock.RUnlock()

	if !ok {
		log.Errorf("no client stream expecting publish response for id %q", resp.GetId())
		return
	}

	select {
	case <-ctx.Done():
	case ch <- resp:
	}
}

func (s *streamconn) registerPublishResponse(id string) (chan *rtv1pb.SubscribeTopicEventsResponse, func()) {
	ch := make(chan *rtv1pb.SubscribeTopicEventsResponse)
	s.lock.Lock()
	s.publishResponses[id] = ch
	s.lock.Unlock()
	return ch, func() {
		s.lock.Lock()
		defer s.lock.Unlock()
		delete(s.publishResponses, id)
	}
}
