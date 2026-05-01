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

package oldserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	fclient "github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/framework/process/ports"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(oldserver))
}

// oldserver verifies the legacy fallback path: a daprd connected to a
// placement server that does NOT include ChangedActorTypes on LOCK
// (pre-rollout server) must run the legacy diff-on-UPDATE path. We stand
// up a hand-rolled gRPC placement server, drive it through a LOCK ->
// UPDATE -> UNLOCK round with ChangedActorTypes left nil, and assert the
// daprd both becomes ready and successfully serves an actor invocation.
type oldserver struct {
	app    *app.App
	daprd  *daprd.Daprd
	fake   *fakePlace
	listen net.Listener
}

func (o *oldserver) Setup(t *testing.T) []framework.Option {
	o.app = app.New(t,
		app.WithConfig(`{"entities": ["actorA"]}`),
		app.WithHandlerFunc("/actors/actorA/", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`OK`))
		}),
	)

	fp := ports.Reserve(t, 1)
	placePort := fp.Port(t)
	fp.Free(t)
	listen, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(placePort))
	require.NoError(t, err)
	o.listen = listen

	o.fake = newFakePlace(t)

	o.daprd = daprd.New(t,
		daprd.WithAppPort(o.app.Port()),
		daprd.WithPlacementAddresses(listen.Addr().String()),
		daprd.WithInMemoryActorStateStore("mystore"),
		// Cap the graceful shutdown so the test ends promptly even when
		// the fake placement is torn down before daprd has finished its
		// actor-drain step.
		daprd.WithDaprGracefulShutdownSeconds(2),
	)

	return []framework.Option{
		framework.WithProcesses(o.fake, o.app, o.daprd),
	}
}

func (o *oldserver) Run(t *testing.T, ctx context.Context) {
	o.fake.serve(t, ctx, o.listen)

	o.daprd.WaitUntilRunning(t, ctx)

	// The daprd must complete one LOCK -> UPDATE -> UNLOCK round driven by
	// the fake placement (with ChangedActorTypes left nil) and become
	// ready. Without the legacy fallback, an old-style LOCK would either
	// be misinterpreted (no-op short-circuit) or block the round; in
	// either case actor invocation would fail.
	require.Eventually(t, func() bool {
		return o.fake.completedRounds.Load() > 0
	}, time.Second*15, time.Millisecond*100,
		"fake placement must complete at least one legacy round")

	// Drive an actor invocation against actorA. Routing requires the
	// daprd to have installed the placement table from the fake's UPDATE
	// frame. Since the table maps actorA to this daprd, the call lands
	// locally.
	httpClient := fclient.HTTP(t)
	url := fmt.Sprintf("http://localhost:%d/v1.0/actors/actorA/myid/method/foo",
		o.daprd.HTTPPort())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	require.NoError(t, err)
	resp, err := httpClient.Do(req)
	require.NoError(t, err)
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode,
		"actor invocation must succeed against a daprd connected to a legacy placement server")
}

// fakePlace is a minimal in-process gRPC placement server that sends LOCK,
// UPDATE, and UNLOCK frames WITHOUT setting ChangedActorTypes, simulating a
// placement binary built before the type-aware-LOCK rollout.
type fakePlace struct {
	v1pb.UnimplementedPlacementServer

	server          *grpc.Server
	completedRounds atomic.Int32

	wg sync.WaitGroup
}

func newFakePlace(_ *testing.T) *fakePlace {
	return &fakePlace{}
}

// Run satisfies the framework process.Interface contract; the actual gRPC
// serve is started by serve below once the test has the listener.
func (f *fakePlace) Run(t *testing.T, ctx context.Context) {}

func (f *fakePlace) Cleanup(t *testing.T) {
	if f.server != nil {
		f.server.GracefulStop()
	}
	f.wg.Wait()
}

func (f *fakePlace) serve(t *testing.T, ctx context.Context, listen net.Listener) {
	t.Helper()

	f.server = grpc.NewServer(grpc.Creds(insecure.NewCredentials()))
	v1pb.RegisterPlacementServer(f.server, f)

	// GracefulStop is invoked by Cleanup, which the framework runs in
	// reverse order of WithProcesses registration, so the fake outlives
	// the daprd. Registering t.Cleanup here directly would invert that
	// order (LIFO across t.Cleanup) and shut the fake down first.
	f.wg.Go(func() {
		if serr := f.server.Serve(listen); serr != nil && !errors.Is(serr, grpc.ErrServerStopped) {
			t.Logf("fake placement server stopped: %v", serr)
		}
	})
}

func (f *fakePlace) ReportDaprStatus(stream v1pb.Placement_ReportDaprStatusServer) error {
	// Read the daprd's initial report.
	host, err := stream.Recv()
	if err != nil {
		return err
	}

	const version uint64 = 1

	// LOCK without ChangedActorTypes (legacy server behaviour).
	if err := stream.Send(&v1pb.PlacementOrder{
		Operation: "lock",
		Version:   version,
	}); err != nil {
		return err
	}
	if _, err := stream.Recv(); err != nil { // ack
		return err
	}

	// UPDATE with a table mapping the daprd's reported entities back to
	// itself, so subsequent actor invocations route locally.
	tables := &v1pb.PlacementTables{
		Entries:           map[string]*v1pb.PlacementTable{},
		ReplicationFactor: 100,
	}
	for _, ent := range host.GetEntities() {
		tables.Entries[ent] = &v1pb.PlacementTable{
			LoadMap: map[string]*v1pb.Host{
				host.GetName(): host,
			},
		}
	}
	if err := stream.Send(&v1pb.PlacementOrder{
		Operation: "update",
		Version:   version,
		Tables:    tables,
	}); err != nil {
		return err
	}
	if _, err := stream.Recv(); err != nil { // ack
		return err
	}

	// UNLOCK.
	if err := stream.Send(&v1pb.PlacementOrder{
		Operation: "unlock",
		Version:   version,
	}); err != nil {
		return err
	}
	if _, err := stream.Recv(); err != nil { // ack
		return err
	}

	f.completedRounds.Add(1)

	// Hold the stream open for any heartbeats / idle reports the daprd may
	// send during the rest of the test.
	for {
		if _, err := stream.Recv(); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}
