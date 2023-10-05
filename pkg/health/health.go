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
	"io"
	"net/http"
<<<<<<< HEAD
	"sync"
	"sync/atomic"
=======
>>>>>>> 22882a74b (Updates pkgs/health to use more aggressive trys)
	"time"

	kclock "k8s.io/utils/clock"

	"github.com/dapr/kit/logger"
)

const (
	defaultInitialDelay      = time.Second * 1
	defaultFailureThreshold  = 2
	defaultRequestTimeout    = time.Second * 2
	defaultHealthyInterval   = time.Second * 3
	defaultUnhealthyInterval = time.Second / 2
	defaultSuccessStatusCode = http.StatusOK
)

// Option is a function that applies a health check option.
type Option func(r *Reporter)

// Reporter is a HTTP client which checks the status of a given endpoint, and
// reports the health on subscribed channels.
type Reporter struct {
	client   *http.Client
	endpoint string
	log      logger.Logger

<<<<<<< HEAD
type healthCheckOptions struct {
	address           string
=======
>>>>>>> 22882a74b (Updates pkgs/health to use more aggressive trys)
	initialDelay      time.Duration
	requestTimeout    time.Duration
	failureThreshold  int32
	healthyInterval   time.Duration
	unhealthyInterval time.Duration
	successStatusCode int

	clock kclock.WithTicker
	ch    chan bool
}

<<<<<<< HEAD
// StartEndpointHealthCheck starts a health check on the specified address with the given options.
// It returns a channel that will emit true if the endpoint is healthy and false if the failure conditions
// have been met.
func StartEndpointHealthCheck(ctx context.Context, opts ...Option) <-chan bool {
	options := &healthCheckOptions{}
	applyDefaults(options)
=======
func New(endpoint string, opts ...Option) *Reporter {
	r := &Reporter{
		client:   http.DefaultClient,
		endpoint: endpoint,
		log: logger.NewLogger("health").
			WithFields(map[string]any{"endpoint": endpoint}),
		failureThreshold:  defaultFailureThreshold,
		initialDelay:      defaultInitialDelay,
		requestTimeout:    defaultRequestTimeout,
		successStatusCode: defaultSuccessStatusCode,
		healthyInterval:   defaultHealthyInterval,
		unhealthyInterval: defaultUnhealthyInterval,
		ch:                make(chan bool),
		clock:             new(kclock.RealClock),
	}

>>>>>>> 22882a74b (Updates pkgs/health to use more aggressive trys)
	for _, o := range opts {
		o(r)
	}
<<<<<<< HEAD

	if options.address == "" {
		panic("required option 'address' is missing")
	}

	signalChan := make(chan bool, 1)

	ticker := options.clock.NewTicker(options.interval)
	ch := ticker.C()

	client := options.client
	if client == nil {
		client = &http.Client{}
	}

	go func() {
		wg := sync.WaitGroup{}
		defer func() {
			wg.Wait()
			close(signalChan)
		}()

		if options.initialDelay > 0 {
			select {
			case <-options.clock.After(options.initialDelay):
			case <-ctx.Done():
			}
		}

		// Initial state is unhealthy until we validate it
		// This makes it so the channel will receive a healthy status on the first successful healthcheck
		failureCount := &atomic.Int32{}
		failureCount.Store(options.failureThreshold)
		signalChan <- false

		for {
			select {
			case <-ch:
				wg.Add(1)
				go func() {
					doHealthCheck(client, options.address, options.requestTimeout, signalChan, failureCount, options)
					wg.Done()
				}()
			case <-ctx.Done():
				return
			}
		}
	}()

	return signalChan
}

func doHealthCheck(client *http.Client, endpointAddress string, timeout time.Duration, signalChan chan bool, failureCount *atomic.Int32, options *healthCheckOptions) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpointAddress, nil)
	if err != nil {
		healthLogger.Errorf("Failed to create healthcheck request: %v", err)
		return
	}

	res, err := client.Do(req)
	if err != nil || res.StatusCode != options.successStatusCode {
		fc := failureCount.Add(1)
		// Check if we've overflown
		if fc < 0 {
			failureCount.Store(options.failureThreshold + 1)
		} else if fc == options.failureThreshold {
			// If we're here, we just passed the threshold right now
			healthLogger.Warn("Actor healthchecks detected unhealthy app")
			signalChan <- false
		}
	} else {
		// Reset the failure count
		// If the previous value was >= threshold, we need to report a health change
		prev := failureCount.Swap(0)
		if prev >= options.failureThreshold {
			healthLogger.Info("Actor healthchecks detected app entering healthy status")
			signalChan <- true
		}
	}
=======

	return r
}

// Run starts the health check. It will signal the health on subscribed health
// channels when the given endpoint becomes either healthy or unhealthy.
func (r *Reporter) Run(ctx context.Context) {
	select {
	case <-r.clock.After(r.initialDelay):
	case <-ctx.Done():
	}

	for {
		if ctx.Err() != nil {
			return
		}

		r.checkUnhealthy(ctx)
		r.checkHealthy(ctx)
	}
}

func (r *Reporter) sendUpdate(ctx context.Context, healthy bool) {
	if ctx.Err() != nil {
		return
	}
	select {
	case <-ctx.Done():
		return
	case r.ch <- healthy:
	}
}

func (r *Reporter) checkHealthy(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}

	ticker := r.clock.NewTicker(r.healthyInterval)
	defer ticker.Stop()
	var unhealthyCount int32
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C():
			if r.check(ctx) {
				unhealthyCount = 0
			} else {
				unhealthyCount++
				if unhealthyCount >= r.failureThreshold {
					r.log.Info("Health check failed")
					r.sendUpdate(ctx, false)
					return
				}
			}
		}
	}
}

func (r *Reporter) checkUnhealthy(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}

	ticker := r.clock.NewTicker(r.unhealthyInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C():
			if r.check(ctx) {
				r.log.Info("Health check succeeded")
				r.sendUpdate(ctx, true)
				return
			} else {
				r.log.Info("Health check failed")
			}
		}
	}
}

func (r *Reporter) Ch() <-chan bool {
	return r.ch
}

func (r *Reporter) check(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, r.requestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.endpoint, nil)
	if err != nil {
		r.log.Errorf("Failed to create healthcheck request: %v", err)
		return false
	}

	res, err := r.client.Do(req)
	if err != nil || res.StatusCode != r.successStatusCode {
		r.log.Debugf("Health check failed: %v", err)
		return false
	}
>>>>>>> 22882a74b (Updates pkgs/health to use more aggressive trys)

	if res != nil {
		// Drain before closing
		_, _ = io.Copy(io.Discard, res.Body)
		_ = res.Body.Close()
	}

	return true
}

// WithAddress sets the endpoint address for the health check.
func WithAddress(address string) Option {
	return func(o *healthCheckOptions) {
		o.address = address
	}
}

// WithInitialDelay sets the initial delay for the health check.
func WithInitialDelay(delay time.Duration) Option {
	return func(r *Reporter) {
		r.initialDelay = delay
	}
}

// WithFailureThreshold sets the failure threshold for the health check.
func WithFailureThreshold(threshold int32) Option {
	return func(r *Reporter) {
		r.failureThreshold = threshold
	}
}

// WithRequestTimeout sets the request timeout for the health check.
func WithRequestTimeout(timeout time.Duration) Option {
	return func(r *Reporter) {
		r.requestTimeout = timeout
	}
}

// WithHTTPClient sets the http.Client to use.
func WithHTTPClient(client *http.Client) Option {
	return func(r *Reporter) {
		r.client = client
	}
}

// WithSuccessStatusCode sets the status code for the health check.
func WithSuccessStatusCode(code int) Option {
	return func(r *Reporter) {
		r.successStatusCode = code
	}
}

// WithHealthyInterval sets the interval for the health check when the endpoint
// is healthy.
func WithHealthyInterval(interval time.Duration) Option {
	return func(r *Reporter) {
		r.healthyInterval = interval
	}
}

// WithUnhealthyInterval sets the interval for the health check when the
// endpoint is unhealthy.
func WithUnhealthyInterval(interval time.Duration) Option {
	return func(r *Reporter) {
		r.unhealthyInterval = interval
	}
}

// WithClock sets a custom clock (for mocking time).
func WithClock(clock kclock.WithTicker) Option {
	return func(r *Reporter) {
		r.clock = clock
	}
}
