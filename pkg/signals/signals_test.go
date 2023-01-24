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

package signals

import (
	"os"
	"os/signal"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func Test_Context(t *testing.T) {
	onlyOneSignalHandler = make(chan struct{})
	t.Cleanup(func() {
		signal.Reset()
	})

	ctx := Context()
	p, err := os.FindProcess(os.Getpid())
	require.NoError(t, err)
	require.NoError(t, p.Signal(os.Interrupt))
	select {
	case <-ctx.Done():
	case <-time.After(1 * time.Second):
		t.Error("context should have closed")
	}
}
