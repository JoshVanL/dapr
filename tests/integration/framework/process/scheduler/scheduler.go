/*
Copyright 2024 The Dapr Authors
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

package scheduler

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework/binary"
	"github.com/dapr/dapr/tests/integration/framework/process"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/util"
)

type Scheduler struct {
	exec     process.Interface
	freeport *util.FreePort
	running  atomic.Bool

	port        int
	healthzPort int
	metricsPort int

	id                  string
	initialCluster      string
	initialClusterPorts []int
	etcdClientPort      int
}

func New(t *testing.T, fopts ...Option) *Scheduler {
	t.Helper()

	uid, err := uuid.NewUUID()
	require.NoError(t, err)

	fp := util.ReservePorts(t, 5)
	opts := options{
		id:                  uid.String(),
		logLevel:            "info",
		port:                fp.Port(t, 0),
		healthzPort:         fp.Port(t, 1),
		metricsPort:         fp.Port(t, 2),
		initialCluster:      uid.String() + "=http://localhost:" + strconv.Itoa(fp.Port(t, 3)),
		initialClusterPorts: []int{fp.Port(t, 3)},
		etcdClientPort:      fp.Port(t, 4),
	}

	for _, fopt := range fopts {
		fopt(&opts)
	}

	args := []string{
		"--log-level=" + opts.logLevel,
		"--id=" + opts.id,
		"--port=" + strconv.Itoa(opts.port),
		"--healthz-port=" + strconv.Itoa(opts.healthzPort),
		"--metrics-port=" + strconv.Itoa(opts.metricsPort),
		"--initial-cluster=" + opts.initialCluster,
		"--tls-enabled=" + strconv.FormatBool(opts.tlsEnabled),
		"--etcd-data-dir=" + t.TempDir(),
		"--etcd-client-port=" + strconv.Itoa(opts.etcdClientPort),
	}

	if opts.listenAddress != nil {
		args = append(args, "--listen-address="+*opts.listenAddress)
	}
	if opts.sentryAddress != nil {
		args = append(args, "--sentry-address="+*opts.sentryAddress)
	}
	if opts.trustAnchorsFile != nil {
		args = append(args, "--trust-anchors-file="+*opts.trustAnchorsFile)
	}

	return &Scheduler{
		exec:                exec.New(t, binary.EnvValue("scheduler"), args, opts.execOpts...),
		freeport:            fp,
		id:                  opts.id,
		port:                opts.port,
		healthzPort:         opts.healthzPort,
		metricsPort:         opts.metricsPort,
		initialCluster:      opts.initialCluster,
		initialClusterPorts: opts.initialClusterPorts,
		etcdClientPort:      opts.etcdClientPort,
	}
}

func (s *Scheduler) Run(t *testing.T, ctx context.Context) {
	if !s.running.CompareAndSwap(false, true) {
		t.Fatal("Process is already running")
	}

	s.freeport.Free(t)
	s.exec.Run(t, ctx)
}

func (s *Scheduler) Cleanup(t *testing.T) {
	if !s.running.CompareAndSwap(true, false) {
		return
	}

	s.exec.Cleanup(t)
}

func (s *Scheduler) WaitUntilRunning(t *testing.T, ctx context.Context) {
	client := util.HTTPClient(t)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://localhost:%d/healthz", s.healthzPort), nil)
	require.NoError(t, err)
	assert.Eventually(t, func() bool {
		resp, err := client.Do(req)
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		return http.StatusOK == resp.StatusCode
	}, time.Second*5, 100*time.Millisecond)
}

func (s *Scheduler) ID() string {
	return s.id
}

func (s *Scheduler) Port() int {
	return s.port
}

func (s *Scheduler) Address() string {
	return "localhost:" + strconv.Itoa(s.port)
}

func (s *Scheduler) HealthzPort() int {
	return s.healthzPort
}

func (s *Scheduler) MetricsPort() int {
	return s.metricsPort
}

func (s *Scheduler) InitialCluster() string {
	return s.initialCluster
}

func (s *Scheduler) EtcdClientPort() int {
	return s.etcdClientPort
}

func (s *Scheduler) InitialClusterPorts() []int {
	return s.initialClusterPorts
}

func (s *Scheduler) ListenAddress() string {
	return "localhost"
}
