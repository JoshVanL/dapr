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

package multiple

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	fclient "github.com/dapr/dapr/tests/integration/framework/client"
	dactors "github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(crosstypelock))
}

// crosstypelock proves that an actor invocation on daprd-B (hosting only
// actorB) is not disrupted while the placement service runs a round whose
// only changed type is actorA (hosted on daprd-A). With the type-aware-LOCK
// optimization, daprd-B's disseminator must short-circuit the round (no
// per-type drain, no HaltNonHosted), keeping its lookup loop responsive to
// new and in-flight actor invocations.
type crosstypelock struct {
	place                *placement.Placement
	actorA               *dactors.Actors
	actorB               *dactors.Actors
	longRunningInFlight  atomic.Int32
	actorBInvocationDone chan struct{}
}

func (c *crosstypelock) Setup(t *testing.T) []framework.Option {
	c.actorBInvocationDone = make(chan struct{})

	c.place = placement.New(t,
		placement.WithDisseminateTimeout(time.Second*30),
	)

	c.actorA = dactors.New(t,
		dactors.WithActorTypes("actorA"),
		dactors.WithPlacement(c.place),
		dactors.WithActorTypeHandler("actorA", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`OK`))
		}),
	)
	c.actorB = dactors.New(t,
		dactors.WithActorTypes("actorB"),
		dactors.WithPeerActor(c.actorA),
		dactors.WithActorTypeHandler("actorB", func(w http.ResponseWriter, r *http.Request) {
			// The "longrunning" actor blocks until the test signals it
			// to finish, simulating an in-flight call that should
			// survive a cross-type round. All other actor IDs return
			// immediately (probe invocations).
			if strings.Contains(r.URL.Path, "/actorB/longrunning/") {
				c.longRunningInFlight.Add(1)
				defer c.longRunningInFlight.Add(-1)
				select {
				case <-r.Context().Done():
				case <-c.actorBInvocationDone:
				case <-time.After(time.Second * 10):
				}
			}
			w.Write([]byte(`OK`))
		}),
	)

	return []framework.Option{
		framework.WithProcesses(c.actorA, c.actorB),
	}
}

func (c *crosstypelock) Run(t *testing.T, ctx context.Context) {
	c.actorA.WaitUntilRunning(t, ctx)
	c.actorB.WaitUntilRunning(t, ctx)

	// Wait for both daprd to register in the placement table.
	assert.EventuallyWithT(t, func(co *assert.CollectT) {
		table := c.place.PlacementTables(t, ctx)
		if !assert.Contains(co, table.Tables, "default") {
			return
		}
		assert.Len(co, table.Tables["default"].Hosts, 2)
	}, time.Second*15, time.Millisecond*100)

	httpClient := fclient.HTTP(t)
	daprdBURL := fmt.Sprintf("http://localhost:%d", c.actorB.Daprd().HTTPPort())

	// Kick off a long-running invocation on daprd-B (actorB).
	type invokeResult struct {
		code int
		err  error
	}
	longRunning := make(chan invokeResult, 1)
	go func() {
		rctx, cancel := context.WithTimeout(ctx, time.Second*15)
		defer cancel()
		req, err := http.NewRequestWithContext(rctx, http.MethodPost,
			daprdBURL+"/v1.0/actors/actorB/longrunning/method/foo", nil)
		if err != nil {
			longRunning <- invokeResult{err: err}
			return
		}
		resp, err := httpClient.Do(req)
		if err != nil {
			longRunning <- invokeResult{err: err}
			return
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		longRunning <- invokeResult{code: resp.StatusCode}
	}()

	// Wait until the long-running actorB handler is in flight before
	// triggering the round.
	require.Eventually(t, func() bool {
		return c.longRunningInFlight.Load() > 0
	}, time.Second*5, time.Millisecond*50,
		"long-running actorB invocation must be in flight")

	// Trigger a placement round whose only changed type is "actorC", which
	// neither real daprd hosts. Use a fake stream so the test owns the
	// round's host lifecycle.
	pclient := c.place.Client(t, ctx)
	fake, err := pclient.ReportDaprStatus(ctx)
	require.NoError(t, err)
	require.NoError(t, fake.Send(&v1pb.Host{
		Name:      "appC",
		Port:      9999,
		Entities:  []string{"actorC"},
		Id:        "appC",
		Namespace: "default",
	}))

	// Echo the placement orders back to drive the round to completion. The
	// placement server falls back to currentOperation when the host's
	// Operation field is UNKNOWN, so we only need to send a basic Host
	// per phase.
	var fakeWG sync.WaitGroup
	fakeWG.Go(func() {
		for {
			if _, recvErr := fake.Recv(); recvErr != nil {
				return
			}
			if serr := fake.Send(&v1pb.Host{
				Name:      "appC",
				Port:      9999,
				Entities:  []string{"actorC"},
				Id:        "appC",
				Namespace: "default",
			}); serr != nil {
				return
			}
		}
	})

	// Wait for the round to complete: placement table must show the new
	// actorC host alongside the existing two daprd.
	assert.EventuallyWithT(t, func(co *assert.CollectT) {
		table := c.place.PlacementTables(t, ctx)
		if !assert.Contains(co, table.Tables, "default") {
			return
		}
		assert.Len(co, table.Tables["default"].Hosts, 3)
	}, time.Second*15, time.Millisecond*100)

	// While the round just completed, fire a steady stream of NEW actorB
	// invocations against daprd-B and assert each completes promptly. If
	// daprd-B's disseminator loop had been blocked by HaltNonHosted on the
	// round, these invocations would queue and miss the per-call deadline.
	const probeCount = 20
	const probeDeadline = time.Second * 2
	for i := range probeCount {
		probeCtx, cancel := context.WithTimeout(ctx, probeDeadline)
		req, perr := http.NewRequestWithContext(probeCtx, http.MethodPost,
			fmt.Sprintf("%s/v1.0/actors/actorB/probe-%d/method/foo", daprdBURL, i), nil)
		require.NoError(t, perr)

		start := time.Now()
		resp, perr := httpClient.Do(req)
		cancel()
		require.NoError(t, perr, "probe %d must not error during cross-type round", i)
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		assert.Less(t, time.Since(start), probeDeadline,
			"probe %d must complete within %s during cross-type round", i, probeDeadline)
		assert.Equal(t, http.StatusOK, resp.StatusCode, "probe %d status", i)
	}

	// Allow the long-running invocation to finish, then verify it succeeded.
	close(c.actorBInvocationDone)
	select {
	case res := <-longRunning:
		require.NoError(t, res.err)
		assert.Equal(t, http.StatusOK, res.code,
			"long-running actorB call begun before the cross-type round must complete successfully")
	case <-time.After(time.Second * 10):
		require.Fail(t, "long-running actorB call did not complete after the cross-type round")
	}

	require.NoError(t, fake.CloseSend())
	fakeWG.Wait()
}
