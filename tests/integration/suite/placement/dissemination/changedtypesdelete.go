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
	suite.Register(new(changedtypesdelete))
}

// changedtypesdelete verifies the delete path: when a daprd disconnects, the
// follow-up LOCK emitted to the surviving streams must advertise the
// disconnected host's prior entity set as the changed actor types. This
// covers the EntitiesOf-before-Delete path in processWaitingDeletes.
type changedtypesdelete struct {
	place *placement.Placement
}

func (c *changedtypesdelete) Setup(t *testing.T) []framework.Option {
	c.place = placement.New(t,
		placement.WithDisseminateTimeout(time.Second*10),
	)
	return []framework.Option{
		framework.WithProcesses(c.place),
	}
}

func (c *changedtypesdelete) Run(t *testing.T, ctx context.Context) {
	c.place.WaitUntilRunning(t, ctx)
	assert.Eventually(t, func() bool {
		return c.place.IsLeader(t, ctx)
	}, time.Second*10, time.Millisecond*10)

	client := c.place.Client(t, ctx)

	// Stream A registers actorA and drives round 1 to completion.
	a, err := client.ReportDaprStatus(ctx)
	require.NoError(t, err)
	require.NoError(t, a.Send(&v1pb.Host{
		Name: "appA", Port: 1001, Entities: []string{"actorA"},
		Id: "appA", Namespace: "default",
	}))
	r, err := a.Recv()
	require.NoError(t, err)
	require.Equal(t, "lock", r.GetOperation())
	completeRound(t, []v1pb.Placement_ReportDaprStatusClient{a}, []*v1pb.Host{
		{Name: "appA", Port: 1001, Entities: []string{"actorA"}, Id: "appA", Namespace: "default"},
	})

	// Stream B (with actorB and actorC) joins. Round 2 LOCK should advertise
	// actorB and actorC.
	b, err := client.ReportDaprStatus(ctx)
	require.NoError(t, err)
	require.NoError(t, b.Send(&v1pb.Host{
		Name: "appB", Port: 1002, Entities: []string{"actorB", "actorC"},
		Id: "appB", Namespace: "default",
	}))
	rA, err := a.Recv()
	require.NoError(t, err)
	require.Equal(t, "lock", rA.GetOperation())
	rB, err := b.Recv()
	require.NoError(t, err)
	require.Equal(t, "lock", rB.GetOperation())
	assert.ElementsMatch(t, []string{"actorB", "actorC"}, rA.GetChangedActorTypes())
	assert.ElementsMatch(t, []string{"actorB", "actorC"}, rB.GetChangedActorTypes())
	completeRound(t,
		[]v1pb.Placement_ReportDaprStatusClient{a, b},
		[]*v1pb.Host{
			{Name: "appA", Port: 1001, Entities: []string{"actorA"}, Id: "appA", Namespace: "default"},
			{Name: "appB", Port: 1002, Entities: []string{"actorB", "actorC"}, Id: "appB", Namespace: "default"},
		},
	)

	// Stream B closes. The follow-up LOCK on stream A must advertise B's
	// prior entity set (actorB, actorC) as the changed types.
	require.NoError(t, b.CloseSend())

	rA, err = a.Recv()
	require.NoError(t, err)
	require.Equal(t, "lock", rA.GetOperation())
	assert.ElementsMatch(t, []string{"actorB", "actorC"}, rA.GetChangedActorTypes(),
		"delete-driven round must list the disconnected host's prior entity set")
}
