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

package integration

import (
	"context"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/binary"
	"github.com/dapr/dapr/tests/integration/suite"

	_ "github.com/dapr/dapr/tests/integration/suite/daprd"
	_ "github.com/dapr/dapr/tests/integration/suite/healthz"
	_ "github.com/dapr/dapr/tests/integration/suite/ports"
)

const (
	defaultConcurrency = 5

	envConcurrency = "DAPR_INTEGRATION_CONCURRENCY"
)

func RunIntegrationTests(t *testing.T) {
	// Parallelise the integration tests, but don't run more than `conc` (default
	// 3) at once.
	conc := concurrency(t)
	t.Logf("running integration tests with concurrency: %d", conc)

	binary.BuildAll(t)

	guard := make(chan struct{}, conc)

	for _, tcase := range suite.All() {
		tcase := tcase
		tof := reflect.TypeOf(tcase).Elem()
		_, aft, ok := strings.Cut(tof.PkgPath(), "tests/integration/suite/")
		require.True(t, ok)
		testName := aft + "/" + tof.Name()

		t.Run(testName, func(t *testing.T) {
			t.Parallel()

			t.Logf("%s: setting up test case", testName)
			options := tcase.Setup(t)

			// Wait for a slot to become available.
			guard <- struct{}{}
			t.Cleanup(func() {
				// Release the slot.
				<-guard
			})

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			f := framework.Run(t, ctx, options...)

			t.Run("run", func(t *testing.T) {
				t.Log("running test case")
				tcase.Run(t, ctx)
			})

			t.Log("cleaning up framework")
			f.Cleanup(t)

			t.Log("done")
		})
	}
}

func concurrency(t *testing.T) int {
	conc := defaultConcurrency
	concS, ok := os.LookupEnv(envConcurrency)
	if ok {
		var err error
		conc, err = strconv.Atoi(concS)
		if err != nil {
			t.Fatalf("failed to parse %q: %s", envConcurrency, err)
		}
		if conc < 1 {
			t.Fatalf("%q must be >= 1", envConcurrency)
		}
	}

	return conc
}
