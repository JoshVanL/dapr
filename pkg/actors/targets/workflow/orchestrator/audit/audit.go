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

// Package audit runs the background integrity sweep for resident workflow
// orchestrator actors. Each sweep re-reads every resident actor's persisted
// state from the state store and re-verifies it against the signature chain,
// detecting state store tampering that the in-memory cache would otherwise
// mask. Detection latency is bounded by the sweep interval.
package audit

import (
	"context"
	"sync"
	"time"
)

// workers bounds how many targets are audited concurrently within one sweep,
// capping the read burst against the state store.
const workers = 4

// Target is a resident actor that can audit its own cached state against the
// state store. Implemented by the orchestrator actor.
type Target interface {
	AuditIntegrity(ctx context.Context)
}

type Options struct {
	// Interval is the period between sweeps. Must be positive.
	Interval time.Duration

	// Targets returns a snapshot of the currently resident actors to audit.
	Targets func() []Target
}

type Auditor struct {
	interval time.Duration
	targets  func() []Target
}

func New(opts Options) *Auditor {
	return &Auditor{
		interval: opts.Interval,
		targets:  opts.Targets,
	}
}

// Run sweeps all resident targets every interval until ctx is cancelled.
// Sweeps are single-flight: a tick cannot fire again until the previous sweep
// returns. Compatible with concurrency.RunnerManager.
func (a *Auditor) Run(ctx context.Context) error {
	ticker := time.NewTicker(a.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			a.sweep(ctx)
		}
	}
}

// sweep audits all current targets through a bounded worker pool, waiting for
// every worker to return before it does.
func (a *Auditor) sweep(ctx context.Context) {
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	defer wg.Wait()

	for _, target := range a.targets() {
		select {
		case <-ctx.Done():
			return
		case sem <- struct{}{}:
		}
		wg.Add(1)
		go func(t Target) {
			defer wg.Done()
			defer func() { <-sem }()
			t.AuditIntegrity(ctx)
		}(target)
	}
}
