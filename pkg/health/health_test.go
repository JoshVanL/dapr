/*
Copyright 2023 The Dapr Authors
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

package health

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	clocktesting "k8s.io/utils/clock/testing"
)

var startOfTime = time.Date(2022, 1, 1, 12, 0, 0, 0, time.UTC)

<<<<<<< HEAD
func TestHealthCheck(t *testing.T) {
	ts := &testServer{}
	server := httptest.NewServer(ts)
	defer server.Close()

	t.Run("unhealthy endpoint, custom interval 1, failure threshold 2s", func(t *testing.T) {
		ts.SetStatusCode(200)

		clock := &clocktesting.FakeClock{}
		clock.SetTime(startOfTime)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		ch := StartEndpointHealthCheck(ctx,
			WithAddress(server.URL),
			WithClock(clock),
			WithInterval(time.Second),
			WithFailureThreshold(2),
			WithInitialDelay(0),
		)

		// First healthcheck is always unsuccessful, right away
		healthy := assertHealthSignal(t, clock, ch)
		assert.False(t, healthy)

		// First actual healthcheck is successful
		clock.Step(time.Second)
		healthy = assertHealthSignal(t, clock, ch)
		assert.True(t, healthy)

		// Set to unsuccessful
		ts.SetStatusCode(500)

		// Nothing happens for the first second
		clock.Step(time.Second)
		assertNoHealthSignal(t, clock, ch)

		// Get a signal after the next tick
		clock.Step(time.Second)
		healthy = assertHealthSignal(t, clock, ch)
		assert.False(t, healthy)
	})

	t.Run("healthy endpoint, custom interval 1s, failure threshold 1, initial delay 2s", func(t *testing.T) {
		ts.SetStatusCode(200)

		clock := &clocktesting.FakeClock{}
		clock.SetTime(startOfTime)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		ch := StartEndpointHealthCheck(ctx,
			WithAddress(server.URL),
			WithClock(clock),
			WithInterval(time.Second),
			WithFailureThreshold(1),
			WithInitialDelay(time.Second*2),
		)

		// Nothing happens for the first 2s
		for i := 0; i < 2; i++ {
			clock.Step(time.Second)
			assertNoHealthSignal(t, clock, ch)
		}

		// Get a signal right away
		clock.Step(time.Second)
		healthy := assertHealthSignal(t, clock, ch)
		assert.False(t, healthy)

		// App recovers after the next tick
		clock.Step(time.Second)
		healthy = assertHealthSignal(t, clock, ch)
		assert.True(t, healthy)
	})

	t.Run("unhealthy endpoint, custom interval 1s, failure threshold 2, initial delay 2s", func(t *testing.T) {
		ts.SetStatusCode(200)

		clock := &clocktesting.FakeClock{}
		clock.SetTime(startOfTime)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		ch := StartEndpointHealthCheck(ctx,
			WithAddress(server.URL),
			WithClock(clock),
			WithInterval(time.Second*1),
			WithFailureThreshold(2),
			WithInitialDelay(time.Second*2),
		)

		// Nothing happens for the first 2s
		for i := 0; i < 2; i++ {
			clock.Step(time.Second)
			assertNoHealthSignal(t, clock, ch)
		}

		// Get a signal right away
		clock.Step(time.Second)
		healthy := assertHealthSignal(t, clock, ch)
		assert.False(t, healthy)

		// App recovers after the next tick
		clock.Step(time.Second)
		healthy = assertHealthSignal(t, clock, ch)
		assert.True(t, healthy)

		// Set to unsuccessful
		ts.SetStatusCode(500)

		// Nothing happens for the first 1s
		clock.Step(time.Second)
		assertNoHealthSignal(t, clock, ch)

		// Get a signal after the next tick
		clock.Step(time.Second)
		healthy = assertHealthSignal(t, clock, ch)
		assert.False(t, healthy)
	})
}

=======
>>>>>>> 22882a74b (Updates pkgs/health to use more aggressive trys)
func TestApplyOptions(t *testing.T) {
	assert.Equal(t, int32(2), int32(defaultFailureThreshold))
	assert.Equal(t, time.Second, defaultInitialDelay)
	assert.Equal(t, time.Second*3, defaultHealthyInterval)
	assert.Equal(t, time.Second/2, defaultUnhealthyInterval)
	assert.Equal(t, time.Second*2, defaultRequestTimeout)
	assert.Equal(t, 200, defaultSuccessStatusCode)

	t.Run("valid defaults", func(t *testing.T) {
		r := New("")

		assert.Equal(t, r.failureThreshold, int32(defaultFailureThreshold))
		assert.Equal(t, r.initialDelay, defaultInitialDelay)
		assert.Equal(t, r.healthyInterval, defaultHealthyInterval)
		assert.Equal(t, r.unhealthyInterval, defaultUnhealthyInterval)
		assert.Equal(t, r.requestTimeout, defaultRequestTimeout)
		assert.Equal(t, r.successStatusCode, defaultSuccessStatusCode)
	})

	t.Run("valid custom options", func(t *testing.T) {
		r := New("",
			WithFailureThreshold(10),
			WithInitialDelay(time.Second*11),
			WithHealthyInterval(time.Second*12),
			WithUnhealthyInterval(time.Second*16),
			WithRequestTimeout(time.Second*13),
			WithSuccessStatusCode(201),
		)

		assert.Equal(t, r.failureThreshold, int32(10))
		assert.Equal(t, r.initialDelay, time.Second*11)
		assert.Equal(t, r.healthyInterval, time.Second*12)
		assert.Equal(t, r.unhealthyInterval, time.Second*16)
		assert.Equal(t, r.requestTimeout, time.Second*13)
		assert.Equal(t, r.successStatusCode, 201)
	})
}

type testServer struct {
	statusCode    atomic.Int32
	numberOfCalls atomic.Int64
}

func (t *testServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	t.numberOfCalls.Add(1)
	w.WriteHeader(int(t.statusCode.Load()))
}

func (t *testServer) SetStatusCode(statusCode int32) {
	t.statusCode.Store(statusCode)
}

func TestResponses(t *testing.T) {
	t.Run("default success status", func(t *testing.T) {
<<<<<<< HEAD
		clock := &clocktesting.FakeClock{}
		clock.SetTime(startOfTime)

		ts := &testServer{}
		ts.SetStatusCode(200)
		server := httptest.NewServer(ts)
		defer server.Close()

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		ch := StartEndpointHealthCheck(ctx,
			WithAddress(server.URL),
			WithClock(clock),
=======
		t.Parallel()

		server := httptest.NewServer(&testServer{
			statusCode: 200,
		})
		t.Cleanup(server.Close)

		clock, ch := runReporter(t, server.URL,
>>>>>>> 22882a74b (Updates pkgs/health to use more aggressive trys)
			WithInitialDelay(0),
			WithFailureThreshold(1),
		)

<<<<<<< HEAD
=======
		// Step the initial delay of 0.
		clock.Step(1)
		require.Eventually(t, clock.HasWaiters, time.Second, time.Microsecond)
		clock.Step(time.Second / 2)
>>>>>>> 22882a74b (Updates pkgs/health to use more aggressive trys)
		healthy := assertHealthSignal(t, clock, ch)
		assert.False(t, healthy)

		clock.Step(5 * time.Second)
		healthy = assertHealthSignal(t, clock, ch)
		assert.True(t, healthy)
	})

	t.Run("custom success status", func(t *testing.T) {
<<<<<<< HEAD
		clock := &clocktesting.FakeClock{}
		clock.SetTime(startOfTime)

		ts := &testServer{}
		ts.SetStatusCode(201)
		server := httptest.NewServer(ts)
		defer server.Close()

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		ch := StartEndpointHealthCheck(ctx,
			WithAddress(server.URL),
			WithClock(clock),
=======
		t.Parallel()

		server := httptest.NewServer(&testServer{
			statusCode: 201,
		})
		t.Cleanup(server.Close)

		clock, ch := runReporter(t, server.URL,
>>>>>>> 22882a74b (Updates pkgs/health to use more aggressive trys)
			WithInitialDelay(0),
			WithFailureThreshold(1),
			WithSuccessStatusCode(201),
		)

<<<<<<< HEAD
=======
		// Step the initial delay of 0.
		clock.Step(1)
		require.Eventually(t, clock.HasWaiters, time.Second, time.Microsecond)
		clock.Step(time.Second / 2)
>>>>>>> 22882a74b (Updates pkgs/health to use more aggressive trys)
		healthy := assertHealthSignal(t, clock, ch)
		assert.False(t, healthy)

		clock.Step(5 * time.Second)
		healthy = assertHealthSignal(t, clock, ch)
		assert.True(t, healthy)
	})

	t.Run("test fail", func(t *testing.T) {
<<<<<<< HEAD
		clock := &clocktesting.FakeClock{}
		clock.SetTime(startOfTime)

		ts := &testServer{}
		ts.SetStatusCode(500)
		server := httptest.NewServer(ts)
		defer server.Close()

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		ch := StartEndpointHealthCheck(ctx,
			WithAddress(server.URL),
			WithClock(clock),
			WithInitialDelay(0),
			WithFailureThreshold(1),
		)
		healthy := assertHealthSignal(t, clock, ch)
		assert.False(t, healthy)

		clock.Step(5 * time.Second)
=======
		t.Parallel()

		server := httptest.NewServer(&testServer{
			statusCode: 500,
		})
		t.Cleanup(server.Close)

		clock, ch := runReporter(t, server.URL,
			WithInitialDelay(0),
			WithFailureThreshold(1),
		)

		// Step the initial delay of 0.
		clock.Step(1)
		require.Eventually(t, clock.HasWaiters, time.Second, time.Microsecond)
		clock.Step(time.Second / 2)
>>>>>>> 22882a74b (Updates pkgs/health to use more aggressive trys)
		assertNoHealthSignal(t, clock, ch)
	})

	t.Run("test app recovery", func(t *testing.T) {
<<<<<<< HEAD
		clock := &clocktesting.FakeClock{}
		clock.SetTime(startOfTime)

		ts := &testServer{}
		ts.SetStatusCode(500)
		server := httptest.NewServer(ts)
		defer server.Close()

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		ch := StartEndpointHealthCheck(ctx,
			WithAddress(server.URL),
			WithClock(clock),
=======
		t.Parallel()

		test := &testServer{
			statusCode: 200,
		}
		server := httptest.NewServer(test)
		t.Cleanup(server.Close)

		clock, ch := runReporter(t, server.URL,
>>>>>>> 22882a74b (Updates pkgs/health to use more aggressive trys)
			WithInitialDelay(0),
			WithFailureThreshold(1),
		)

<<<<<<< HEAD
		healthy := assertHealthSignal(t, clock, ch)
		assert.False(t, healthy)

		for i := 0; i <= 1; i++ {
			clock.Step(5 * time.Second)
			if i == 0 {
				assertNoHealthSignal(t, clock, ch)
				ts.SetStatusCode(200)
			} else {
				healthy = assertHealthSignal(t, clock, ch)
				assert.True(t, healthy)
			}
=======
		clock.Step(1)
		require.Eventually(t, clock.HasWaiters, time.Second, time.Microsecond)
		clock.Step(time.Second / 2)
		healthy := assertHealthSignal(t, clock, ch)
		assert.True(t, healthy)

		test.statusCode = 500
		require.Eventually(t, clock.HasWaiters, time.Second, time.Microsecond)
		clock.Step(time.Second * 3)
		healthy = assertHealthSignal(t, clock, ch)
		assert.False(t, healthy)

		test.statusCode = 200
		require.Eventually(t, clock.HasWaiters, time.Second, time.Microsecond)
		clock.Step(time.Second / 2)
		healthy = assertHealthSignal(t, clock, ch)
		assert.True(t, healthy)
	})
}

func runReporter(t *testing.T, endpoint string, opts ...Option) (*clocktesting.FakeClock, <-chan bool) {
	t.Helper()

	clock := new(clocktesting.FakeClock)
	clock.SetTime(startOfTime)

	reporter := New(endpoint, append(opts, WithClock(clock))...)

	ch := reporter.Ch()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		reporter.Run(ctx)
	}()

	require.Eventually(t, clock.HasWaiters, time.Second, time.Millisecond*50, "ticker in program not created in time")

	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			assert.Fail(t, "timed out waiting for reporter to stop")
>>>>>>> 22882a74b (Updates pkgs/health to use more aggressive trys)
		}
	})

	return clock, ch
}

func assertHealthSignal(t *testing.T, clock *clocktesting.FakeClock, ch <-chan bool) bool {
	t.Helper()
	// Wait to ensure ticker in health server is setup.
	// Wait for the clock to have tickers before stepping, since they are likely
	// being created in another go routine to this test.
	require.Eventually(t, clock.HasWaiters, time.Second, time.Microsecond, "ticker in program not created in time")

	select {
	case v := <-ch:
		return v
	case <-time.After(200 * time.Millisecond):
		t.Fatal("did not receive a signal in 200ms")
	}
	return false
}

func assertNoHealthSignal(t *testing.T, clock *clocktesting.FakeClock, ch <-chan bool) {
	t.Helper()

	// Wait to ensure ticker in health server is setup.
	// Wait for the clock to have tickers before stepping, since they are likely
	// being created in another go routine to this test.
	require.Eventually(t, clock.HasWaiters, time.Second, time.Microsecond, "ticker in program not created in time")

	// The signal is sent in a background goroutine, so we need to use a wall clock here
	select {
	case <-ch:
		t.Fatal("received unexpected signal")
	case <-time.After(200 * time.Millisecond):
		// all good
	}
}
