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

package binary

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func BuildAll(t *testing.T) {
	t.Helper()

	binaryNames := []string{"daprd", "placement", "sentry"}

	err := make(chan error)
	for _, name := range binaryNames {
		go func(name string) {
			err <- Build(t, name)
		}(name)
	}

	for i := 0; i < len(binaryNames); i++ {
		require.NoError(t, <-err)
	}
}

func Build(t *testing.T, name string) error {
	if _, ok := os.LookupEnv(EnvKey(name)); !ok {
		t.Logf("%q not set, building %q binary", EnvKey(name), name)

		_, tfile, _, ok := runtime.Caller(0)
		if !ok {
			return errors.New("failed to get caller info")
		}
		rootDir := filepath.Join(filepath.Dir(tfile), "../../../..")

		// Use a consistent temp dir for the binary so that the binary is cached on
		// subsequent runs.
		binPath := filepath.Join(os.TempDir(), "dapr_integration_tests/"+name)
		if runtime.GOOS == "windows" {
			binPath += ".exe"
		}

		// Ensure CGO is disabled to avoid linking against system libraries.
		if err := os.Setenv("CGO_ENABLED", "0"); err != nil {
			return err
		}

		t.Logf("Root dir: %q", rootDir)
		t.Logf("Compiling %q binary to: %q", name, binPath)
		cmd := exec.Command("go", "build", "-tags=allcomponents", "-v", "-o", binPath, filepath.Join(rootDir, "cmd/"+name))
		cmd.Dir = rootDir
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return err
		}

		return os.Setenv(EnvKey(name), binPath)
	}

	return nil
}

func EnvValue(name string) string {
	return os.Getenv(EnvKey(name))
}

func EnvKey(name string) string {
	return fmt.Sprintf("DAPR_INTEGRATION_%s_PATH", strings.ToUpper(name))
}
