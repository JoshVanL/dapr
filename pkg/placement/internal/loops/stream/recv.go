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
	"fmt"

	"github.com/google/go-cmp/cmp"

	"github.com/dapr/dapr/pkg/placement/internal/loops"
	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
)

// recvLoop is the main loop for receiving messages from the stream. It handles
// errors and calls the recv function to receive messages.
func (s *stream) recvLoop() error {
	for {
		err := s.recv()
		if err != nil {
			log.Warnf("Error receiving from stream %s", s.addr)
			return err
		}
	}
}

// recv receives a message from the stream. It checks whether this host needs
// to disseminate the namespace.
func (s *stream) recv() error {
	resp, err := s.channel.Recv()
	if err != nil {
		return err
	}

	// TODO: @joshvanl: we can potentially cache the client ID from the stream
	// context after the first message.
	if err = s.authz.Host(s.channel, resp); err != nil {
		log.Warnf("Authorization failed for stream %s: %v", s.addr, err)
		return err
	}

	resp.Namespace = s.ns
	s.loop.Enqueue(resp)

	return nil
}

// handleReceive processes incoming messages from the stream.
func (s *stream) handleRecive(resp *v1pb.Host) {
	if !s.shouldReport(resp) {
		fmt.Printf(">>GOT HOST WHICH DOES NOT NEED REPORTING: %+v\n", resp.Entities)
		return
	}

	fmt.Printf(">>GOT HOST WHICH NEEDS REPORTING %d: %+v\n", s.idx, resp.Entities)

	s.host = resp
	s.nsLoop.Enqueue(&loops.ReportedHost{
		Host:p,
		StreamIDx:
	})
}

func (s *stream) shouldReport(h *v1pb.Host) bool {
	if s.currentVersion == nil {
		return true
	}

	// TODO: @joshvanl
	if v := h.Version; v != nil && *v < *s.currentVersion {
		// Ignore reports of old operation versions.
		return false
	}

	// If `operations` are set, we are talking to a new client that supports
	// operations. Honor the operation specified.
	if op := h.Operation; op != nil {
		if *op != s.currentOperation {
			// Ignore out-of-order operations.
			return false
		}

		// Always report non-report operations.
		if *op != v1pb.HostOperation_UNLOCK {
			return true
		}
	}

	// Always return true if we are in a locking operation to account for old
	// clients.
	if s.currentOperation != v1pb.HostOperation_UNLOCK {
		return true
	}

	return s.needsDissemination(h)
}

func (s *stream) needsDissemination(h *v1pb.Host) bool {
	m := s.host

	if m == nil {
		fmt.Printf(">>NO CACHED HOST, NEEDS DISSEMINATION\n")
		return true
	}

	todoJosh := !(m.GetId() == h.GetId() &&
		m.GetName() == h.GetName() &&
		cmp.Equal(m.GetEntities(), h.GetEntities()))

	fmt.Printf(">RETURNING NEEDS DISSEMINATION: %v\n", todoJosh)

	return todoJosh
}
