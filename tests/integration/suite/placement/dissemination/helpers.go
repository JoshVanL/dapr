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

package dissemination

import (
	"testing"

	"github.com/stretchr/testify/require"

	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
)

// completeRound walks the LOCK/UPDATE/UNLOCK ack handshake on every stream so
// the placement server can advance phases. Each stream's host is sent verbatim
// at every phase ack. Assumes streams are already at the LOCK-received state
// for the current round.
func completeRound(t *testing.T, streams []v1pb.Placement_ReportDaprStatusClient, hosts []*v1pb.Host) {
	t.Helper()
	require.Len(t, hosts, len(streams), "host count must match stream count")

	// Ack LOCK so server advances to UPDATE.
	for i, s := range streams {
		require.NoError(t, s.Send(hosts[i]))
	}
	for i, s := range streams {
		r, err := s.Recv()
		require.NoError(t, err, "stream %d update", i)
		require.Equal(t, "update", r.GetOperation())
	}
	// Ack UPDATE so server advances to UNLOCK.
	for i, s := range streams {
		require.NoError(t, s.Send(hosts[i]))
	}
	for i, s := range streams {
		r, err := s.Recv()
		require.NoError(t, err, "stream %d unlock", i)
		require.Equal(t, "unlock", r.GetOperation())
	}
	// Ack UNLOCK so server returns to REPORT.
	for i, s := range streams {
		require.NoError(t, s.Send(hosts[i]))
	}
}
