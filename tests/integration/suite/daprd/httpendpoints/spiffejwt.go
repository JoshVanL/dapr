/*
Copyright 2026 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implieh.
See the License for the specific language governing permissions and
limitations under the License.
*/

package httpendpoints

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/app"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
	cryptotest "github.com/dapr/kit/crypto/test"
)

func init() {
	suite.Register(new(spiffejwt))
}

type spiffejwt struct {
	daprd *daprd.Daprd
}

func (s *spiffejwt) Setup(t *testing.T) []framework.Option {
	sentry := sentry.New(t,
		sentry.WithTrustDomain("integration.test.dapr.io"),
		sentry.WithEnableJWT(true),
	)

	certs := cryptotest.GenPKI(t, cryptotest.PKIOptions{LeafDNS: "localhost"})

	srv := prochttp.New(t,
		prochttp.WithHandlerFunc("/hello", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(r.Header.Get("X-My-Auth")))
		}),
		prochttp.WithTLS(t, certs.LeafCertPEM, certs.LeafPKPEM),
	)

	app := app.New(t)

	s.daprd = daprd.New(t,
		daprd.WithAppPort(app.Port(t)),
		daprd.WithSentry(t, sentry),
		daprd.WithControlPlaneTrustDomain("integration.test.dapr.io"),
		daprd.WithResourceFiles(`
apiVersion: dapr.io/v1alpha1
kind: HTTPEndpoint
metadata:
  name: foo
spec:
  version: v1alpha1
  baseURL: "https://localhost:`+strconv.Itoa(srv.Port())+`"
  spiffe:
    jwt:
      header: "X-My-Auth"
      audience: "public"
  clientTLS:
    rootCA:
      value: "`+strings.ReplaceAll(string(certs.RootCertPEM), "\n", "\\n")+`"
`),
	)

	return []framework.Option{
		framework.WithProcesses(srv, app, sentry, s.daprd),
	}
}

func (s *spiffejwt) Run(t *testing.T, ctx context.Context) {
	s.daprd.WaitUntilRunning(t, ctx)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, s.daprd.GetMetaHTTPEndpoints(t, ctx), 1)
	}, time.Second*5, time.Millisecond*10)

	client := client.HTTP(t)

	req, err := http.NewRequestWithContext(ctx,
		http.MethodGet,
		fmt.Sprintf("http://%s/v1.0/invoke/foo/method/hello", s.daprd.HTTPAddress()),
		nil,
	)
	require.NoError(t, err)

	resp, err := client.Do(req)
	require.NoError(t, err)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	ptoken, err := jwt.ParseInsecure(body)
	require.NoError(t, err)

	assert.Equal(t,
		"spiffe://public/ns/default/"+s.daprd.AppID(),
		ptoken.Subject(),
	)
	assert.Equal(t, []string{"public"}, ptoken.Audience())
}
