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

package http

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/suite"
	cryptotest "github.com/dapr/kit/crypto/test"
)

func init() {
	suite.Register(new(appTLS))
}

// appTLS tests that daprd can communicate with an HTTPS app using
// InsecureSkipTLSVerify (self-signed cert, no CA provided).
type appTLS struct {
	daprd *procdaprd.Daprd
}

func (a *appTLS) Setup(t *testing.T) []framework.Option {
	certs := cryptotest.GenPKI(t, cryptotest.PKIOptions{LeafDNS: "localhost"})

	srv := prochttp.New(t,
		prochttp.WithHandlerFunc("/hello", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("Hello TLS"))
		}),
		prochttp.WithTLS(t, certs.LeafCertPEM, certs.LeafPKPEM),
	)

	a.daprd = procdaprd.New(t,
		procdaprd.WithAppPort(srv.Port()),
		procdaprd.WithAppProtocol("https"),
	)

	return []framework.Option{
		framework.WithProcesses(srv, a.daprd),
	}
}

func (a *appTLS) Run(t *testing.T, ctx context.Context) {
	a.daprd.WaitUntilRunning(t, ctx)

	httpClient := client.HTTP(t)

	t.Run("invoke app over HTTPS with self-signed cert", func(t *testing.T) {
		reqURL := fmt.Sprintf("http://%s/v1.0/invoke/%s/method/hello",
			a.daprd.HTTPAddress(), a.daprd.AppID())
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		require.NoError(t, err)

		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode, "expected 200 OK but got %d: %s", resp.StatusCode, string(body))
		assert.Equal(t, "Hello TLS", string(body))
	})
}
