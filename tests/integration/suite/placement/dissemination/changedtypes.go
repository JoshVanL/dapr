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
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(changedtypes))
}

// changedtypes verifies the type-aware LOCK signal: when a daprd reports a
// change to its actor types, the placement server emits LOCK to every stream
// in the namespace with `ChangedActorTypes` set to the symmetric difference
// of the reporting host's prior and new entity sets. This is the wire-side
// proof of the optimization that lets a daprd whose hosted types do not
// overlap with the changed set short-circuit the round on UPDATE/UNLOCK.
type changedtypes struct {
	place *placement.Placement
}

func (c *changedtypes) Setup(t *testing.T) []framework.Option {
	c.place = placement.New(t,
		placement.WithDisseminateTimeout(time.Second*10),
	)

	return []framework.Option{
		framework.WithProcesses(c.place),
	}
}

func (c *changedtypes) Run(t *testing.T, ctx context.Context) {
	c.place.WaitUntilRunning(t, ctx)

	assert.Eventually(t, func() bool {
		return c.place.IsLeader(t, ctx)
	}, time.Second*10, time.Millisecond*10)

	client := c.place.Client(t, ctx)

	// Stream A (app A) registers actorA. This drives round 1.
	a, err := client.ReportDaprStatus(ctx)
	require.NoError(t, err)
	require.NoError(t, a.Send(&v1pb.Host{
		Name:      "appA",
		Port:      1001,
		Entities:  []string{"actorA"},
		Id:        "appA",
		Namespace: "default",
	}))

	// Round 1 LOCK: actorA is the only changed type (host A is brand new).
	r, err := a.Recv()
	require.NoError(t, err)
	require.Equal(t, "lock", r.GetOperation())
	assert.ElementsMatch(t, []string{"actorA"}, r.GetChangedActorTypes(),
		"round 1 LOCK must list actorA as the only changed type")

	completeRound(t, []v1pb.Placement_ReportDaprStatusClient{a}, []*v1pb.Host{
		{Name: "appA", Port: 1001, Entities: []string{"actorA"}, Id: "appA", Namespace: "default"},
	})

	// Stream B (app B) joins with actorB. This drives round 2 in which the
	// server LOCK frame should advertise actorB as the only changed type to
	// both A (existing stream) and B (new stream).
	b, err := client.ReportDaprStatus(ctx)
	require.NoError(t, err)
	require.NoError(t, b.Send(&v1pb.Host{
		Name:      "appB",
		Port:      1002,
		Entities:  []string{"actorB"},
		Id:        "appB",
		Namespace: "default",
	}))

	rA, err := a.Recv()
	require.NoError(t, err)
	require.Equal(t, "lock", rA.GetOperation())
	rB, err := b.Recv()
	require.NoError(t, err)
	require.Equal(t, "lock", rB.GetOperation())

	assert.ElementsMatch(t, []string{"actorB"}, rA.GetChangedActorTypes(),
		"existing stream A must see actorB (and only actorB) as the changed type")
	assert.ElementsMatch(t, []string{"actorB"}, rB.GetChangedActorTypes(),
		"new stream B must see actorB (and only actorB) as the changed type")
	assert.Equal(t, rA.GetVersion(), rB.GetVersion(),
		"both streams must see the same round version")

	completeRound(t, []v1pb.Placement_ReportDaprStatusClient{a, b}, []*v1pb.Host{
		{Name: "appA", Port: 1001, Entities: []string{"actorA"}, Id: "appA", Namespace: "default"},
		{Name: "appB", Port: 1002, Entities: []string{"actorB"}, Id: "appB", Namespace: "default"},
	})

	// App A churns: replaces actorA with actorA2 (symmetric diff: {actorA, actorA2}).
	// Stream B does not host either type, so a daprd implementation can use
	// this LOCK signal to short-circuit the round locally.
	require.NoError(t, a.Send(&v1pb.Host{
		Name:      "appA",
		Port:      1001,
		Entities:  []string{"actorA2"},
		Id:        "appA",
		Namespace: "default",
	}))

	rA, err = a.Recv()
	require.NoError(t, err)
	require.Equal(t, "lock", rA.GetOperation())
	rB, err = b.Recv()
	require.NoError(t, err)
	require.Equal(t, "lock", rB.GetOperation())

	assert.ElementsMatch(t, []string{"actorA", "actorA2"}, rA.GetChangedActorTypes(),
		"app A churn must surface both removed and added types in LOCK")
	assert.ElementsMatch(t, []string{"actorA", "actorA2"}, rB.GetChangedActorTypes(),
		"app B (non-hosting) must receive the same changed-types list to drive its short-circuit decision")
	assert.NotContains(t, rB.GetChangedActorTypes(), "actorB",
		"unaffected types must NOT appear in the changed-types list")
}
