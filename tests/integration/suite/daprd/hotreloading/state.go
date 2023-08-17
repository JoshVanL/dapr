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

package hotreloading

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/util"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(state))
}

type state struct {
	daprd  *procdaprd.Daprd
	client *http.Client

	resDir1 string
	resDir2 string
	resDir3 string
}

func (s *state) Setup(t *testing.T) []framework.Option {
	configFile := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configFile, []byte(`
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: hotreloading
spec:
  features:
  - name: HotReload
    enabled: true`), 0o600))

	s.resDir1, s.resDir2, s.resDir3 = t.TempDir(), t.TempDir(), t.TempDir()
	s.client = util.HTTPClient(t)

	s.daprd = procdaprd.New(t,
		procdaprd.WithConfigs(configFile),
		procdaprd.WithResourcesDir(s.resDir1, s.resDir2, s.resDir3),
	)

	return []framework.Option{
		framework.WithProcesses(s.daprd),
	}
}

func (s *state) Run(t *testing.T, ctx context.Context) {
	s.daprd.WaitUntilRunning(t, ctx)

	t.Run("expect no components to be loaded yet", func(t *testing.T) {
		assert.Len(t, getMetaComponents(t, ctx, s.client, s.daprd.HTTPPort()), 0)
		s.writeExpectError(t, ctx, "123", http.StatusInternalServerError)
	})

	t.Run("adding a component should become available", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join(s.resDir1, "1.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: '123'
spec:
  type: state.in-memory
  version: v1
  metadata: []
`), 0o600))

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Len(c, getMetaComponents(t, ctx, s.client, s.daprd.HTTPPort()), 1)
		}, time.Second*5, time.Millisecond*100)
		resp := getMetaComponents(t, ctx, s.client, s.daprd.HTTPPort())
		require.Len(t, resp, 1)

		assert.Equal(t, &runtimev1pb.RegisteredComponents{
			Name:    "123",
			Type:    "state.in-memory",
			Version: "v1",
			Capabilities: []string{
				"ETAG",
				"TRANSACTIONAL",
				"TTL",
				"ACTOR",
			},
		}, resp[0])

		s.writeRead(t, ctx, "123")
	})

	t.Run("adding a second and third component should also become available", func(t *testing.T) {
		// After a single state store exists, Dapr returns a Bad Request response
		// rather than an Internal Server Error when writing to a non-existent
		// state store.
		s.writeExpectError(t, ctx, "abc", http.StatusBadRequest)
		s.writeExpectError(t, ctx, "xyz", http.StatusBadRequest)

		require.NoError(t, os.WriteFile(filepath.Join(s.resDir2, "2.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: 'abc'
spec:
 type: state.in-memory
 version: v1
 metadata: []
`), 0o600))

		require.NoError(t, os.WriteFile(filepath.Join(s.resDir3, "3.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: 'xyz'
spec:
 type: state.in-memory
 version: v1
 metadata: []
 `), 0o600))

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Len(c, getMetaComponents(t, ctx, s.client, s.daprd.HTTPPort()), 3)
		}, time.Second*5, time.Millisecond*100)
		resp := getMetaComponents(t, ctx, s.client, s.daprd.HTTPPort())
		require.Len(t, resp, 3)

		assert.ElementsMatch(t, []*runtimev1pb.RegisteredComponents{
			{
				Name: "123", Type: "state.in-memory", Version: "v1",
				Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "ACTOR"},
			},
			{
				Name: "abc", Type: "state.in-memory", Version: "v1",
				Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "ACTOR"},
			},
			{
				Name: "xyz", Type: "state.in-memory", Version: "v1",
				Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "ACTOR"},
			},
		}, resp)

		s.writeRead(t, ctx, "123")
		s.writeRead(t, ctx, "abc")
		s.writeRead(t, ctx, "xyz")
	})

	t.Run("changing the type of a state store should update the component and still be available", func(t *testing.T) {
		s.writeRead(t, ctx, "123")
		s.writeRead(t, ctx, "abc")
		s.writeRead(t, ctx, "xyz")

		require.NoError(t, os.WriteFile(filepath.Join(s.resDir2, "2.yaml"), []byte(fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: 'abc'
spec:
 type: state.sqlite
 version: v1
 metadata:
 - name: connectionString
   value: %s/db.sqlite
`, s.resDir2)), 0o600))

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			resp := getMetaComponents(c, ctx, s.client, s.daprd.HTTPPort())
			assert.ElementsMatch(c, []*runtimev1pb.RegisteredComponents{
				{
					Name: "123", Type: "state.in-memory", Version: "v1",
					Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "ACTOR"},
				},
				{
					Name: "abc", Type: "state.sqlite", Version: "v1",
					Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "ACTOR"},
				},
				{
					Name: "xyz", Type: "state.in-memory", Version: "v1",
					Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "ACTOR"},
				},
			}, resp)
		}, time.Second*5, time.Millisecond*100)

		s.writeRead(t, ctx, "123")
		s.writeRead(t, ctx, "abc")
		s.writeRead(t, ctx, "xyz")
	})

	t.Run("updating multiple state stores should be updated, and multiple components in a single file", func(t *testing.T) {
		s.writeRead(t, ctx, "123")
		s.writeRead(t, ctx, "abc")
		s.writeRead(t, ctx, "xyz")
		s.writeExpectError(t, ctx, "foo", http.StatusBadRequest)

		require.NoError(t, os.WriteFile(filepath.Join(s.resDir1, "1.yaml"), []byte(fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: '123'
spec:
 type: state.sqlite
 version: v1
 metadata:
 - name: connectionString
   value: %s/db.sqlite
---
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: 'foo'
spec:
 type: state.in-memory
 version: v1
 metadata: []
`, s.resDir1)), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(s.resDir2, "2.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: 'abc'
spec:
 type: state.in-memory
 version: v1
 metadata: []
`), 0o600))

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			resp := getMetaComponents(c, ctx, s.client, s.daprd.HTTPPort())
			assert.ElementsMatch(c, []*runtimev1pb.RegisteredComponents{
				{
					Name: "123", Type: "state.sqlite", Version: "v1",
					Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "ACTOR"},
				},
				{
					Name: "abc", Type: "state.in-memory", Version: "v1",
					Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "ACTOR"},
				},
				{
					Name: "xyz", Type: "state.in-memory", Version: "v1",
					Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "ACTOR"},
				},
				{
					Name: "foo", Type: "state.in-memory", Version: "v1",
					Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "ACTOR"},
				},
			}, resp)
		}, time.Second*5, time.Millisecond*100)

		s.writeRead(t, ctx, "123")
		s.writeRead(t, ctx, "abc")
		s.writeRead(t, ctx, "xyz")
		s.writeRead(t, ctx, "foo")
	})

	t.Run("renaming a component should close the old name, and open the new one", func(t *testing.T) {
		s.writeRead(t, ctx, "123")
		s.writeRead(t, ctx, "abc")
		s.writeRead(t, ctx, "xyz")
		s.writeRead(t, ctx, "foo")

		require.NoError(t, os.WriteFile(filepath.Join(s.resDir2, "2.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: 'bar'
spec:
 type: state.in-memory
 version: v1
 metadata: []
`), 0o600))

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			resp := getMetaComponents(c, ctx, s.client, s.daprd.HTTPPort())
			assert.ElementsMatch(c, []*runtimev1pb.RegisteredComponents{
				{
					Name: "123", Type: "state.sqlite", Version: "v1",
					Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "ACTOR"},
				},
				{
					Name: "bar", Type: "state.in-memory", Version: "v1",
					Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "ACTOR"},
				},
				{
					Name: "xyz", Type: "state.in-memory", Version: "v1",
					Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "ACTOR"},
				},
				{
					Name: "foo", Type: "state.in-memory", Version: "v1",
					Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "ACTOR"},
				},
			}, resp)
		}, time.Second*5, time.Millisecond*100)

		s.writeRead(t, ctx, "123")
		s.writeRead(t, ctx, "bar")
		s.writeRead(t, ctx, "xyz")
		s.writeRead(t, ctx, "foo")
	})

	t.Run("deleting a component file should delete the components", func(t *testing.T) {
		s.writeRead(t, ctx, "123")
		s.writeRead(t, ctx, "bar")
		s.writeRead(t, ctx, "xyz")
		s.writeRead(t, ctx, "foo")

		require.NoError(t, os.Remove(filepath.Join(s.resDir2, "2.yaml")))

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			resp := getMetaComponents(c, ctx, s.client, s.daprd.HTTPPort())
			assert.ElementsMatch(c, []*runtimev1pb.RegisteredComponents{
				{
					Name: "123", Type: "state.sqlite", Version: "v1",
					Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "ACTOR"},
				},
				{
					Name: "xyz", Type: "state.in-memory", Version: "v1",
					Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "ACTOR"},
				},
				{
					Name: "foo", Type: "state.in-memory", Version: "v1",
					Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "ACTOR"},
				},
			}, resp)
		}, time.Second*5, time.Millisecond*100)

		s.writeRead(t, ctx, "123")
		s.writeExpectError(t, ctx, "bar", http.StatusBadRequest)
		s.writeRead(t, ctx, "xyz")
		s.writeRead(t, ctx, "foo")
	})

	t.Run("deleting all components should result in no components remaining", func(t *testing.T) {
		s.writeRead(t, ctx, "123")
		s.writeExpectError(t, ctx, "bar", http.StatusBadRequest)
		s.writeRead(t, ctx, "xyz")
		s.writeRead(t, ctx, "foo")

		require.NoError(t, os.Remove(filepath.Join(s.resDir1, "1.yaml")))
		require.NoError(t, os.Remove(filepath.Join(s.resDir3, "3.yaml")))

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			resp := getMetaComponents(c, ctx, s.client, s.daprd.HTTPPort())
			assert.ElementsMatch(c, []*runtimev1pb.RegisteredComponents{}, resp)
		}, time.Second*5, time.Millisecond*100)

		s.writeExpectError(t, ctx, "123", http.StatusInternalServerError)
		s.writeExpectError(t, ctx, "bar", http.StatusInternalServerError)
		s.writeExpectError(t, ctx, "xyz", http.StatusInternalServerError)
		s.writeExpectError(t, ctx, "foo", http.StatusInternalServerError)
	})
}

func (s *state) writeExpectError(t *testing.T, ctx context.Context, compName string, expCode int) {
	t.Helper()

	postURL := fmt.Sprintf("http://localhost:%d/v1.0/state/%s", s.daprd.HTTPPort(), compName)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, postURL, nil)
	require.NoError(t, err)
	s.doReq(t, req, expCode, fmt.Sprintf(
		`\{"errorCode":"(ERR_STATE_STORE_NOT_CONFIGURED|ERR_STATE_STORE_NOT_FOUND)","message":"state store (is not configured|%s is not found)"\}`,
		compName))
}

func (s *state) writeRead(t *testing.T, ctx context.Context, compName string) {
	t.Helper()

	postURL := fmt.Sprintf("http://localhost:%d/v1.0/state/%s", s.daprd.HTTPPort(), url.QueryEscape(compName))
	getURL := fmt.Sprintf("%s/foo", postURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, postURL,
		strings.NewReader(`[{"key": "foo", "value": "bar"}]`))
	require.NoError(t, err)
	s.doReq(t, req, http.StatusNoContent, "")

	req, err = http.NewRequestWithContext(ctx, http.MethodGet, getURL, nil)
	require.NoError(t, err)
	s.doReq(t, req, http.StatusOK, `"bar"`)
}

func (s *state) doReq(t *testing.T, req *http.Request, expCode int, expBody string) {
	t.Helper()

	resp, err := s.client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, expCode, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Regexp(t, expBody, string(body))
}
