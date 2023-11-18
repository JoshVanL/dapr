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

package selfhosted

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/util"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(lock))
}

type lock struct {
	daprd  *daprd.Daprd
	client *http.Client

	resDir string
}

func (l *lock) Setup(t *testing.T) []framework.Option {
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

	l.resDir = t.TempDir()
	l.client = util.HTTPClient(t)

	l.daprd = daprd.New(t,
		daprd.WithConfigs(configFile),
		daprd.WithResourcesDir(l.resDir),
	)

	return []framework.Option{
		framework.WithProcesses(l.daprd),
	}
}

func (l *lock) Run(t *testing.T, ctx context.Context) {
	l.daprd.WaitUntilRunning(t, ctx)

	conn, err := grpc.DialContext(ctx, fmt.Sprintf("localhost:%d", l.daprd.GRPCPort()),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })

	client := rtv1.NewDaprClient(conn)

	t.Run("expect no components to be loaded yet", func(t *testing.T) {
		resp, err := client.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
		require.NoError(t, err)
		assert.Len(t, resp.RegisteredComponents, 1)
	})
}
