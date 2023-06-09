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

package state

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"

	fuzz "github.com/google/gofuzz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/validation/path"

	"github.com/dapr/dapr/tests/integration/framework"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(fuzzstate))
}

type saveReqBinary struct {
	Key   string `json:"key"`
	Value []byte `json:"value"`
}

type saveReqString struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type fuzzstate struct {
	daprd *procdaprd.Daprd

	storeName       string
	getFuzzKeys     []string
	saveReqBinaries [][]saveReqBinary
	saveReqStrings  [][]saveReqString
}

func (f *fuzzstate) Setup(t *testing.T) []framework.Option {
	var takenKeys sync.Map

	fuzzFuncs := []any{
		func(s *saveReqBinary, c fuzz.Continue) {
			var ok bool
			for len(s.Key) == 0 || strings.Contains(s.Key, "||") || ok {
				s.Key = c.RandString()
				_, ok = takenKeys.LoadOrStore(s.Key, true)
			}
			for len(s.Value) == 0 {
				c.Fuzz(&s.Value)
			}
		},
		func(s *saveReqString, c fuzz.Continue) {
			var ok bool
			for len(s.Key) == 0 || strings.Contains(s.Key, "||") || ok {
				s.Key = c.RandString()
				_, ok = takenKeys.LoadOrStore(s.Key, true)
			}
			for len(s.Value) == 0 {
				s.Value = c.RandString()
			}
		},
		func(s *string, c fuzz.Continue) {
			var ok bool
			for len(*s) == 0 || ok {
				*s = c.RandString()
				_, ok = takenKeys.LoadOrStore(*s, true)
			}
		},
	}

	for f.storeName == "" {
		fuzz.New().Fuzz(&f.storeName)
	}

	f.daprd = procdaprd.New(t, procdaprd.WithComponentFiles(fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: %s
spec:
  type: state.in-memory
  version: v1
`, f.storeName)))

	f.getFuzzKeys = make([]string, 1000)
	f.saveReqBinaries = make([][]saveReqBinary, 1000)
	f.saveReqStrings = make([][]saveReqString, 1000)

	fz := fuzz.New().Funcs(fuzzFuncs...)
	for i := 0; i < 1000; i++ {
		fz.Fuzz(&f.getFuzzKeys[i])
		if strings.Contains(f.getFuzzKeys[i], "||") || len(path.IsValidPathSegmentName(f.getFuzzKeys[i])) > 0 {
			f.getFuzzKeys[i] = ""
			i--
		}
	}
	for i := 0; i < 1000; i++ {
		fz.Fuzz(&f.saveReqBinaries[i])
		fz.Fuzz(&f.saveReqStrings[i])
	}

	return []framework.Option{
		framework.WithProcesses(f.daprd),
	}
}

func (f *fuzzstate) Run(t *testing.T, ctx context.Context) {
	f.daprd.WaitUntilRunning(t)

	t.Run("get", func(t *testing.T) {
		t.Parallel()
		for i := range f.getFuzzKeys {
			getURL := fmt.Sprintf("http://localhost:%d/v1.0/state/%s/%s", f.daprd.HTTPPort(), url.QueryEscape(f.storeName), url.QueryEscape(f.getFuzzKeys[i]))
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, getURL, nil)
			require.NoError(t, err)
			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			assert.Equal(t, http.StatusNoContent, resp.StatusCode)
			respBody, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.NoError(t, resp.Body.Close())
			assert.Empty(t, string(respBody), "key: %s", f.getFuzzKeys[i])
		}
	})

	for i := 0; i < 1000; i++ {
		i := i
		t.Run("save "+strconv.Itoa(i), func(t *testing.T) {
			t.Parallel()
			for _, req := range []any{f.saveReqBinaries[i], f.saveReqStrings[i]} {
				postURL := fmt.Sprintf("http://localhost:%d/v1.0/state/%s", f.daprd.HTTPPort(), url.QueryEscape(f.storeName))
				b := new(bytes.Buffer)
				require.NoError(t, json.NewEncoder(b).Encode(req))
				req, err := http.NewRequestWithContext(ctx, http.MethodPost, postURL, b)
				require.NoError(t, err)
				resp, err := http.DefaultClient.Do(req)
				require.NoError(t, err)
				assert.Equalf(t, http.StatusNoContent, resp.StatusCode, "key: %s", url.QueryEscape(f.storeName))
				respBody, err := io.ReadAll(resp.Body)
				require.NoError(t, err)
				require.NoError(t, resp.Body.Close())
				assert.Empty(t, string(respBody))
			}

			for _, s := range f.saveReqBinaries[i] {
				getURL := fmt.Sprintf("http://localhost:%d/v1.0/state/%s/%s", f.daprd.HTTPPort(), url.QueryEscape(f.storeName), url.QueryEscape(s.Key))
				req, err := http.NewRequestWithContext(ctx, http.MethodGet, getURL, nil)
				require.NoError(t, err)
				resp, err := http.DefaultClient.Do(req)
				require.NoError(t, err)
				assert.Equal(t, http.StatusOK, resp.StatusCode)
				// Value is base64 encoded when in binary format.
				respBody, err := io.ReadAll(resp.Body)
				require.NoError(t, err)
				val := `"` + base64.StdEncoding.EncodeToString(s.Value) + `"`
				assert.Equalf(t, val, string(respBody), "key: %s, %s", s.Key, req.URL.String())

			}

			// TODO: fix encoding bug
			//for _, s := range f.saveReqStrings[i] {
			//	getURL := fmt.Sprintf("http://localhost:%d/v1.0/state/%s/%s", f.daprd.HTTPPort(), url.QueryEscape(f.storeName), url.QueryEscape(s.Key))
			//	req, err := http.NewRequestWithContext(ctx, http.MethodGet, getURL, nil)
			//	require.NoError(t, err)
			//	resp, err := http.DefaultClient.Do(req)
			//	require.NoError(t, err)
			//	assert.Equal(t, http.StatusOK, resp.StatusCode)
			//	respBody, err := io.ReadAll(resp.Body)
			//	require.NoError(t, err)
			//	orig := append(append([]byte(`"`), s.Value...), []byte(`"`)...)
			//	assert.Equalf(t, orig, respBody, "orig=%q got=%q", s.Value, respBody)
			//}
		})
	}
}
