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
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

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

type header struct {
	name  string
	value string
}

type fuzzhttp struct {
	daprd1 *procdaprd.Daprd
	daprd2 *procdaprd.Daprd

	methods []string
	//bodies  [][]byte
	bodies  []string
	headers [][]header
	queries []map[string]string
}

func (f *fuzzhttp) Setup(t *testing.T) []framework.Option {
	handler := http.NewServeMux()
	handler.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(err.Error()))
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write(body)
	})

	srv := prochttp.New(t, prochttp.WithHandler(handler))
	f.daprd1 = procdaprd.New(t, procdaprd.WithAppPort(srv.Port()), procdaprd.WithLogLevel("info"))
	f.daprd2 = procdaprd.New(t, procdaprd.WithLogLevel("info"))

	var (
		pathChars        = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-._~:/#[]@!$'()+,=")
		headerNameChars  = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-._~")
		headerValueChars = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789a_ :;.,\\/\"'?!(){}[]@<>=-+*#$&`|~^%")
		queryChars       = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-._~:/#[]@!$&'()*+,=")
	)

	methodFuzz := func(s *string, c fuzz.Continue) {
		n := c.Rand.Intn(200)
		var sb strings.Builder
		sb.Grow(n)
		firstSegment := true
		for i := 0; i < n; i++ {
			c := pathChars[c.Rand.Intn(len(pathChars))]
			// Prevent the first character being a '.'.
			if c == '.' && firstSegment && (i == 0 || sb.String()[i-1] == '/') {
				i--
				continue
			}
			// Prevent first segment from having a colon.
			if firstSegment && c == ':' {
				i--
				continue
			}
			if c == '/' && !(i == 0 || (i > 0 && sb.String()[i-1] == '/')) {
				firstSegment = false
			}
			sb.WriteRune(c)
			// Prevent last character being a '.'.
			if i == n-1 && c == '.' {
				i--
				continue
			}
		}
		*s = sb.String()
	}
	headerFuzz := func(s *header, c fuzz.Continue) {
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
	}
	queryFuzz := func(m map[string]string, c fuzz.Continue) {
		n := c.Rand.Intn(100) + 1
		for i := 0; i < n; i++ {
			var sb strings.Builder
			sb.Grow(n)
			for i := 0; i < n; i++ {
				sb.WriteRune(queryChars[c.Rand.Intn(len(queryChars))])
			}
			m[sb.String()] = sb.String()
		}
	}

	f.methods = make([]string, 1000)
	//f.bodies = make([][]byte, 1000)
	f.bodies = make([]string, 1000)
	f.headers = make([][]header, 1000)
	f.queries = make([]map[string]string, 1000)
	t.Run("", func(t *testing.T) {
		t.Parallel()
		for i := 0; i < 1000; i++ {
			fz := fuzz.New()
			fz.NumElements(0, 100).Funcs(methodFuzz).Fuzz(&f.methods[i])
			fz.NumElements(0, 100).Fuzz(&f.bodies[i])
			fz.NumElements(0, 10).Funcs(headerFuzz).Fuzz(&f.headers[i])
			fz.NumElements(0, 100).Funcs(queryFuzz).Fuzz(&f.queries[i])
		}
	})

	return []framework.Option{
		framework.WithProcesses(f.daprd1, f.daprd2, srv),
	}
}

func (f *fuzzhttp) Run(t *testing.T, ctx context.Context) {
	f.daprd1.WaitUntilRunning(t)
	f.daprd2.WaitUntilRunning(t)

	for i := 0; i < 1000; i++ {
		method := f.methods[i]
		body := f.bodies[i]
		headers := f.headers[i]
		query := f.queries[i]
		t.Run("method="+method, func(t *testing.T) {
			t.Parallel()
			for _, ts := range []struct {
				url     string
				headers map[string]string
			}{
				{url: fmt.Sprintf("http://localhost:%d/v1.0/invoke/%s/method/%s", f.daprd2.HTTPPort(), f.daprd1.AppID(), method)},
				{url: fmt.Sprintf("http://localhost:%d/%s", f.daprd2.HTTPPort(), method), headers: map[string]string{"dapr-app-id": f.daprd1.AppID()}},
			} {
				req, err := http.NewRequestWithContext(ctx, http.MethodPost, ts.url, strings.NewReader(body))
				require.NoError(t, err)
				for _, header := range headers {
					req.Header.Set(header.name, header.value)
				}
				for k, v := range ts.headers {
					req.Header.Set(k, v)
				}
				for k, v := range query {
					req.URL.Query().Set(k, v)
				}

				resp, err := http.DefaultClient.Do(req)
				require.NoError(t, err)
				respBody, err := io.ReadAll(resp.Body)
				require.NoError(t, err)
				require.NoError(t, resp.Body.Close())
				assert.Equal(t, http.StatusOK, resp.StatusCode)
				assert.Equalf(t, string(body), string(respBody), "url=%s, headers=%v", ts.url, req.Header)
			}
		})
	}
}
