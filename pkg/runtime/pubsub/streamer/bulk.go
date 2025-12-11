/*
Copyright 2025 The Dapr Authors
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

package streamer

import (
	"context"
	"errors"
	"io"

	rtv1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	rtpubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *streamer) BulkSubscribe(stream rtv1pb.Dapr_BulkSubscribeTopicEventsAlpha1Server, req *rtv1pb.BulkSubscribeTopicEventsRequestInitialAlpha1, connectionID rtpubsub.ConnectionID) error {
	s.lock.Lock()
	key := s.StreamerKey(req.GetPubsubName(), req.GetTopic())

	connection := &conn[rtv1pb.Dapr_BulkSubscribeTopicEventsAlpha1Server, *rtv1pb.BulkSubscribeTopicEventsRequestProcessedAlpha1]{
		stream:           stream,
		connectionID:     connectionID,
		closeCh:          make(chan struct{}),
		publishResponses: make(PublishResponses[*rtv1pb.BulkSubscribeTopicEventsRequestProcessedAlpha1]),
	}
	if s.bulkSubscribers[key] == nil {
		s.bulkSubscribers[key] = make(ConnectionsBulk)
	}
	s.bulkSubscribers[key][connectionID] = connection

	log.Infof("Subscribing to pubsub '%s' topic '%s' ConnectionID %d", req.GetPubsubName(), req.GetTopic(), connectionID)
	s.lock.Unlock()

	defer func() {
		s.lock.Lock()
		select {
		case <-connection.closeCh:
		default:
			close(connection.closeCh)
		}
		if connections, ok := s.bulkSubscribers[key]; ok {
			delete(connections, connectionID)
			if len(connections) == 0 {
				delete(s.bulkSubscribers, key)
			}
		}
		s.lock.Unlock()
	}()

	// TODO: @joshvanl: remove after pubsub refactor.
	errCh := make(chan error, 2)
	go func() {
		select {
		case <-s.closeCh:
			connection.lock.Lock()
			connection.closed.Store(true)
			if len(connection.publishResponses) == 0 {
				errCh <- errors.New("stream closed")
			}
			connection.lock.Unlock()
		case <-connection.closeCh:
		case <-stream.Context().Done():
		}
	}()

	go func() {
		var err error
		select {
		case <-connection.closeCh:
			err = errors.New("stream closed")
		case <-stream.Context().Done():
			err = stream.Context().Err()
		}
		errCh <- err
	}()

	go func() {
		errCh <- s.bulkRecvLoop(stream, req, connection)
	}()

	return <-errCh
}

func (s *streamer) bulkRecvLoop(
	stream rtv1pb.Dapr_BulkSubscribeTopicEventsAlpha1Server,
	req *rtv1pb.BulkSubscribeTopicEventsRequestInitialAlpha1,
	conn *conn[rtv1pb.Dapr_BulkSubscribeTopicEventsAlpha1Server, *rtv1pb.BulkSubscribeTopicEventsRequestProcessedAlpha1],
) error {
	for {
		resp, err := stream.Recv()

		stat, ok := status.FromError(err)

		if (ok && stat.Code() == codes.Canceled) ||
			errors.Is(err, context.Canceled) ||
			errors.Is(err, io.EOF) {
			log.Infof("Unsubscribed from pubsub '%s' topic '%s'", req.GetPubsubName(), req.GetTopic())
			return err
		}

		if err != nil {
			log.Errorf("Error receiving message from client stream: %s", err)
			return err
		}

		eventResp := resp.GetEventProcessed()
		if eventResp == nil {
			return errors.New("duplicate initial request received")
		}

		conn.notifyPublishResponse(eventResp)
	}
}
