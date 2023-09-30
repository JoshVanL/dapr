/*
Copyright 2023 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or impliei.
See the License for the specific language governing permissions and
limitations under the License.
*/

package healthz

import (
	"context"
	"net"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/util"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(initerror))
}

// initerror tests that Daprd will block actor calls until actors have been
// initialized.
type initerror struct {
	daprd         *daprd.Daprd
	place         *placement.Placement
	configCalled  chan struct{}
	blockConfig   chan struct{}
	healthzCalled chan struct{}
}

func (i *initerror) Setup(t *testing.T) []framework.Option {
	i.configCalled = make(chan struct{})
	i.blockConfig = make(chan struct{})
	i.healthzCalled = make(chan struct{})

	handler := http.NewServeMux()
	handler.HandleFunc("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
		close(i.configCalled)
		<-i.blockConfig
		w.Write([]byte(`{"entities": ["myactortype"]}`))
	})
	handler.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	handler.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`OK`))
	})

	srv := prochttp.New(t, prochttp.WithHandler(handler))
	i.place = placement.New(t)
	i.daprd = daprd.New(t, daprd.WithResourceFiles(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mystore
spec:
  type: state.in-memory
  version: v1
  metadata:
  - name: actorStateStore
    value: true
`),
		daprd.WithPlacementAddresses("localhost:"+strconv.Itoa(i.place.Port())),
		daprd.WithAppProtocol("http"),
		daprd.WithAppPort(srv.Port()),
		// Daprd is super noisy in debug mode when connecting to placement.
		daprd.WithLogLevel("info"),
	)

	return []framework.Option{
		framework.WithProcesses(i.place, srv, i.daprd),
	}
}

func (i *initerror) Run(t *testing.T, ctx context.Context) {
	i.place.WaitUntilRunning(t, ctx)

	assert.Eventually(t, func() bool {
		dialer := net.Dialer{Timeout: time.Second}
		net, err := dialer.DialContext(ctx, "tcp", "localhost:"+strconv.Itoa(i.daprd.HTTPPort()))
		if err != nil {
			return false
		}
		net.Close()
		return true
	}, time.Second*5, time.Millisecond*100)

	client := util.HTTPClient(t)

	select {
	case <-i.configCalled:
	case <-time.After(time.Second * 5):
		t.Fatal("timed out waiting for config call")
	}

	rctx, cancel := context.WithTimeout(ctx, time.Second*2)
	t.Cleanup(cancel)
	daprdURL := "http://localhost:" + strconv.Itoa(i.daprd.HTTPPort()) + "/v1.0/actors/myactortype/myactorid/method/foo"

	req, err := http.NewRequestWithContext(rctx, http.MethodPost, daprdURL, nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	if resp != nil && resp.Body != nil {
		assert.NoError(t, resp.Body.Close())
	}

	close(i.blockConfig)

	select {
	case <-i.healthzCalled:
	case <-time.After(time.Second * 5):
		t.Fatal("timed out waiting for healthz call")
	}

	req, err = http.NewRequestWithContext(ctx, http.MethodPost, daprdURL, nil)
	require.NoError(t, err)
	resp, err = client.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.NoError(t, resp.Body.Close())
}
