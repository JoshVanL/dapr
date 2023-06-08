/*
Copyright 2023 The Dapr Authors
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

package serviceinvocation

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strings"
	"testing"
	"time"

	fuzz "github.com/google/gofuzz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(fuzzhttp))
}

type fuzzhttp struct {
	daprd1 *procdaprd.Daprd
	daprd2 *procdaprd.Daprd
}

func (f *fuzzhttp) Setup(t *testing.T) []framework.Option {
	handler := http.NewServeMux()
	handler.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.NoError(t, r.Body.Close())
		w.Write(body)
	})

	srv := prochttp.New(t, prochttp.WithHandler(handler))
	f.daprd1 = procdaprd.New(t, procdaprd.WithAppPort(srv.Port()), procdaprd.WithLogLevel("info"))
	f.daprd2 = procdaprd.New(t, procdaprd.WithLogLevel("info"))

	return []framework.Option{
		framework.WithProcesses(f.daprd1, f.daprd2, srv),
	}
}

func (f *fuzzhttp) Run(t *testing.T, ctx context.Context) {
	f.daprd1.WaitUntilRunning(t)
	f.daprd2.WaitUntilRunning(t)

	var (
		pathChars        = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-._~:/?#[]@!$&'()*+,=")
		headerNameChars  = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-._~")
		headerValueChars = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789a_ :;.,\\/\"'?!(){}[]@<>=-+*#$&`|~^%")
	)

	type header struct {
		name  string
		value string
	}

	for i := 0; i < 1000; i++ {
		var (
			method  string
			body    []byte
			headers []header
		)

		fz := fuzz.New().RandSource(rand.NewSource(time.Now().Unix()))
		fz.NumElements(0, 100).Funcs(func(s *string, c fuzz.Continue) {
			n := c.Rand.Intn(1000)
			var sb strings.Builder
			sb.Grow(n)
			firstSegment := true
			for i := 0; i < n; i++ {
				c := pathChars[c.Rand.Intn(len(pathChars))]
				if firstSegment && c == ':' {
					i--
					continue
				}
				if c == '/' {
					firstSegment = false
				}
				sb.WriteRune(c)
			}
			*s = sb.String()
		}).Fuzz(&method)
		fz.NumElements(0, 100).Fuzz(&body)
		fz.NumElements(0, 10).Funcs(func(s *header, c fuzz.Continue) {
			n := c.Rand.Intn(100) + 1
			var sb strings.Builder
			sb.Grow(n)
			for i := 0; i < n; i++ {
				sb.WriteRune(headerNameChars[c.Rand.Intn(len(headerNameChars))])
			}
			s.name = sb.String()
			sb.Reset()
			sb.Grow(n)
			for i := 0; i < n; i++ {
				sb.WriteRune(headerValueChars[c.Rand.Intn(len(headerValueChars))])
			}
			s.value = sb.String()
		}).Fuzz(&headers)

		for _, ts := range []struct {
			url     string
			headers map[string]string
		}{
			{url: fmt.Sprintf("http://localhost:%d/v1.0/invoke/%s/method/%s", f.daprd2.HTTPPort(), f.daprd1.AppID(), method)},
			{url: fmt.Sprintf("http://localhost:%d/%s", f.daprd2.HTTPPort(), method), headers: map[string]string{"dapr-app-id": f.daprd1.AppID()}},
		} {
			req, err := http.NewRequest(http.MethodPost, ts.url, bytes.NewReader(body))
			require.NoError(t, err)
			for _, header := range headers {
				req.Header.Set(header.name, header.value)
			}
			for k, v := range ts.headers {
				req.Header.Set(k, v)
			}
			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			respBody, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.NoError(t, resp.Body.Close())
			assert.Equal(t, http.StatusOK, resp.StatusCode)
			assert.Equal(t, string(body), string(respBody))
		}
	}
}
