/*
Copyright 2026 The Dapr Authors
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

// Package clientops contains integration tests for client-initiated cross-app
// workflow operations: a client connected to app B issues an operation
// against an instance hosted on app A, with same-namespace placement
// performing the cross-app routing. Each operation has its own file.
package clientops

import (
	_ "github.com/dapr/dapr/tests/integration/suite/daprd/workflow/crossapp/clientops/get"
	_ "github.com/dapr/dapr/tests/integration/suite/daprd/workflow/crossapp/clientops/pauseresume"
	_ "github.com/dapr/dapr/tests/integration/suite/daprd/workflow/crossapp/clientops/purge"
	_ "github.com/dapr/dapr/tests/integration/suite/daprd/workflow/crossapp/clientops/raiseevent"
	_ "github.com/dapr/dapr/tests/integration/suite/daprd/workflow/crossapp/clientops/schedule"
	_ "github.com/dapr/dapr/tests/integration/suite/daprd/workflow/crossapp/clientops/terminate"
)
