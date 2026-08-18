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

package audit

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeTarget struct {
	audits   atomic.Int64
	inFlight *atomic.Int64
	maxSeen  *atomic.Int64
	block    time.Duration
}

func (f *fakeTarget) AuditIntegrity(ctx context.Context) {
	if f.inFlight != nil {
		cur := f.inFlight.Add(1)
		for {
			seen := f.maxSeen.Load()
			if cur <= seen || f.maxSeen.CompareAndSwap(seen, cur) {
				break
			}
		}
		defer f.inFlight.Add(-1)
	}
	if f.block > 0 {
		select {
		case <-ctx.Done():
		case <-time.After(f.block):
		}
	}
	f.audits.Add(1)
}

func Test_Run_sweepsTargetsAndStopsOnCancel(t *testing.T) {
	t.Parallel()

	target := &fakeTarget{}
	a := New(Options{
		Interval: 10 * time.Millisecond,
		Targets:  func() []Target { return []Target{target} },
	})

	ctx, cancel := context.WithCancel(t.Context())
	var wg sync.WaitGroup
	errCh := make(chan error, 1)
	wg.Go(func() {
		errCh <- a.Run(ctx)
	})

	assert.Eventually(t, func() bool {
		return target.audits.Load() >= 2
	}, 5*time.Second, 5*time.Millisecond, "targets must be swept repeatedly")

	cancel()
	wg.Wait()
	require.NoError(t, <-errCh)
}

func Test_Run_boundsSweepConcurrency(t *testing.T) {
	t.Parallel()

	var inFlight, maxSeen atomic.Int64
	targets := make([]Target, 16)
	fakes := make([]*fakeTarget, 16)
	for i := range targets {
		fakes[i] = &fakeTarget{inFlight: &inFlight, maxSeen: &maxSeen, block: 20 * time.Millisecond}
		targets[i] = fakes[i]
	}

	a := New(Options{
		Interval: 5 * time.Millisecond,
		Targets:  func() []Target { return targets },
	})

	ctx, cancel := context.WithCancel(t.Context())
	var wg sync.WaitGroup
	wg.Go(func() {
		//nolint:errcheck
		a.Run(ctx)
	})

	assert.Eventually(t, func() bool {
		for _, f := range fakes {
			if f.audits.Load() == 0 {
				return false
			}
		}
		return true
	}, 10*time.Second, 10*time.Millisecond, "every target must be audited")

	cancel()
	wg.Wait()

	assert.LessOrEqual(t, maxSeen.Load(), int64(workers), "sweep concurrency must be bounded by the worker pool")
}
