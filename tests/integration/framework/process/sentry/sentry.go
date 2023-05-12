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

package sentry

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/sentry/ca"
	"github.com/dapr/dapr/pkg/sentry/certs"
	"github.com/dapr/dapr/tests/integration/framework/binary"
	"github.com/dapr/dapr/tests/integration/framework/freeport"
	"github.com/dapr/dapr/tests/integration/framework/process"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
)

// options contains the options for running Sentry in integration tests.
type options struct {
	execOpts []exec.Option

	ca          *certs.Credentials
	rootPEM     []byte
	certPEM     []byte
	keyPEM      []byte
	port        int
	healthzPort int
	metricsPort int
}

// Option is a function that configures the process.
type Option func(*options)

type Sentry struct {
	exec     process.Interface
	freeport *freeport.FreePort

	CA          *certs.Credentials
	Port        int
	HealthzPort int
	MetricsPort int
}

func New(t *testing.T, fopts ...Option) *Sentry {
	t.Helper()

	caPK, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	creds, rootPEM, certPEM, issuerKeyPEM, err := ca.GetNewSelfSignedCertificates(caPK, time.Minute, time.Second*5)
	require.NoError(t, err)

	fp := freeport.New(t, 3)
	opts := options{
		ca:          creds,
		rootPEM:     rootPEM,
		certPEM:     certPEM,
		keyPEM:      issuerKeyPEM,
		port:        fp.Port(t, 0),
		healthzPort: fp.Port(t, 1),
		metricsPort: fp.Port(t, 2),
	}

	for _, fopt := range fopts {
		fopt(&opts)
	}

	configPath := filepath.Join(t.TempDir(), "sentry-config.yaml")
	require.NoError(t, os.WriteFile(configPath, nil, 0o600))

	tmpDir := t.TempDir()
	caPath := filepath.Join(tmpDir, "ca.crt")
	issuerKeyPath := filepath.Join(tmpDir, "issuer.key")
	issuerCertPath := filepath.Join(tmpDir, "issuer.crt")

	for _, pair := range []struct {
		path string
		data []byte
	}{
		{caPath, opts.rootPEM},
		{issuerKeyPath, opts.keyPEM},
		{issuerCertPath, opts.certPEM},
	} {
		require.NoError(t, os.WriteFile(pair.path, pair.data, 0o600))
	}

	args := []string{
		"-log-level=" + "debug",
		"-port=" + strconv.Itoa(opts.port),
		"-config=" + configPath,
		"-issuer-ca-filename=ca.crt",
		"-issuer-certificate-filename=issuer.crt",
		"-issuer-key-filename=issuer.key",
		"-metrics-port=" + strconv.Itoa(opts.metricsPort),
		"-healthz-port=" + strconv.Itoa(opts.healthzPort),
		"-issuer-credentials=" + tmpDir,
	}

	return &Sentry{
		exec:        exec.New(t, binary.EnvValue("sentry"), args, opts.execOpts...),
		freeport:    fp,
		CA:          opts.ca,
		Port:        opts.port,
		MetricsPort: opts.metricsPort,
		HealthzPort: opts.healthzPort,
	}
}

func (s *Sentry) Run(t *testing.T, ctx context.Context) {
	s.freeport.Free(t)
	s.exec.Run(t, ctx)
}

func (s *Sentry) Cleanup(t *testing.T) {
	s.exec.Cleanup(t)
}
