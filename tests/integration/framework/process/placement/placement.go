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

package placement

import (
	"context"
	"strconv"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework/binary"
	"github.com/dapr/dapr/tests/integration/framework/freeport"
	"github.com/dapr/dapr/tests/integration/framework/process"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
)

// options contains the options for running Placement in integration tests.
type options struct {
	execOpts []exec.Option

	id                  string
	port                int
	healthzPort         int
	metricsPort         int
	initialCluster      string
	initialClusterPorts []int
}

// Option is a function that configures the process.
type Option func(*options)

type Placement struct {
	exec     process.Interface
	freeport *freeport.FreePort

	ID                  string
	Port                int
	HealthzPort         int
	MetricsPort         int
	InitialCluster      string
	InitialClusterPorts []int
}

func New(t *testing.T, fopts ...Option) *Placement {
	t.Helper()

	uid, err := uuid.NewUUID()
	require.NoError(t, err)

	fp := freeport.New(t, 4)
	opts := options{
		id:                  uid.String(),
		port:                fp.Port(t, 0),
		healthzPort:         fp.Port(t, 1),
		metricsPort:         fp.Port(t, 2),
		initialCluster:      uid.String() + "=localhost:" + strconv.Itoa(fp.Port(t, 3)),
		initialClusterPorts: []int{fp.Port(t, 3)},
	}

	for _, fopt := range fopts {
		fopt(&opts)
	}

	args := []string{
		"--log-level=" + "debug",
		"--id=" + opts.id,
		"--port=" + strconv.Itoa(opts.port),
		"--healthz-port=" + strconv.Itoa(opts.healthzPort),
		"--metrics-port=" + strconv.Itoa(opts.metricsPort),
		"--initial-cluster=" + opts.initialCluster,
	}

	return &Placement{
		exec:                exec.New(t, binary.EnvValue("placement"), args, opts.execOpts...),
		freeport:            fp,
		ID:                  opts.id,
		Port:                opts.port,
		HealthzPort:         opts.healthzPort,
		MetricsPort:         opts.metricsPort,
		InitialCluster:      opts.initialCluster,
		InitialClusterPorts: opts.initialClusterPorts,
	}
}

func (p *Placement) Run(t *testing.T, ctx context.Context) {
	p.freeport.Free(t)
	p.exec.Run(t, ctx)
}

func (p *Placement) Cleanup(t *testing.T) {
	p.exec.Cleanup(t)
}
