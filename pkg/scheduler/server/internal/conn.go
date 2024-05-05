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

package internal

import (
	"context"
	"errors"
	"io"
	"sync"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
)

type conn struct {
	pool  *Pool
	jobCh chan *schedulerv1pb.WatchJobsResponse

	inflight map[uint32]chan struct{}

	lock    sync.RWMutex
	closeCh chan struct{}
}

func (p *Pool) newConn(req *schedulerv1pb.WatchJobsRequestInitial, stream schedulerv1pb.Scheduler_WatchJobsServer, uuid uint64) *conn {
	conn := &conn{
		pool:     p,
		closeCh:  make(chan struct{}),
		inflight: make(map[uint32]chan struct{}),
		jobCh:    make(chan *schedulerv1pb.WatchJobsResponse, 10),
	}

	p.wg.Add(2)

	go func() {
		defer p.wg.Done()
		defer p.remove(req, uuid)
		for {
			select {
			case job := <-conn.jobCh:
				if err := stream.Send(job); err != nil {
					log.Warnf("Error sending job to connection: %v", err)
				}
			case <-p.closeCh:
				close(conn.closeCh)
				return
			case <-stream.Context().Done():
				close(conn.closeCh)
				return
			case <-conn.closeCh:
				return
			}
		}
	}()

	go func() {
		defer p.wg.Done()

		for {
			resp, err := stream.Recv()
			if errors.Is(err, context.Canceled) || errors.Is(err, io.EOF) {
				return
			}
			if err != nil {
				log.Warnf("Error receiving from connection: %v", err)
				return
			}

			conn.handleJobProcessed(resp.GetResult().GetUuid())
		}
	}()

	return conn
}

func (c *conn) sendWaitForResponse(ctx context.Context, job *schedulerv1pb.WatchJobsResponse) {
	ackCh := make(chan struct{})
	c.lock.Lock()
	c.inflight[job.GetUuid()] = ackCh
	c.lock.Unlock()

	defer func() {
		c.lock.Lock()
		delete(c.inflight, job.GetUuid())
		c.lock.Unlock()
	}()

	select {
	case c.jobCh <- job:
	case <-c.pool.closeCh:
	case <-c.closeCh:
	case <-ctx.Done():
	}

	select {
	case <-ackCh:
	case <-c.pool.closeCh:
	case <-c.closeCh:
	case <-ctx.Done():
	}
}

func (c *conn) handleJobProcessed(id uint32) {
	c.lock.Lock()
	defer c.lock.Unlock()

	if ch, ok := c.inflight[id]; ok {
		select {
		case ch <- struct{}{}:
		case <-c.closeCh:
		case <-c.pool.closeCh:
		}
	}

	delete(c.inflight, id)
}
