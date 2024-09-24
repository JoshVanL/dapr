/*
Copyright 2024 The Dapr Authors
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

package actor

import (
	"context"
	"runtime"
	"testing"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	"google.golang.org/grpc"
)

type Actor struct {
	app   *app.App
	db    *sqlite.SQLite
	place *placement.Placement
	sched *scheduler.Scheduler
	daprd *daprd.Daprd
}

func New(t *testing.T, fopts ...Option) *Actor {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("Skipping test on Windows due to SQLite limitations")
	}

	var opts options
	for _, fopt := range fopts {
		fopt(&opts)
	}

	dbOpts := []sqlite.Option{
		sqlite.WithActorStateStore(true),
		sqlite.WithCreateStateTables(),
	}
	if opts.dbPath != nil {
		dbOpts = append(dbOpts, sqlite.WithDBPath(*opts.dbPath))
	}
	db := sqlite.New(t, dbOpts...)

	app := app.New(t)
	place := placement.New(t)

	dopts := []daprd.Option{
		daprd.WithAppPort(app.Port()),
		daprd.WithPlacementAddresses(place.Address()),
		daprd.WithResourceFiles(db.GetComponent(t)),
	}

	var sched *scheduler.Scheduler
	if opts.enableScheduler {
		sched = scheduler.New(t)
		dopts = append(dopts,
			daprd.WithScheduler(sched),
			daprd.WithConfigManifests(t, `
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: appconfig
spec:
  features:
  - name: SchedulerReminders
    enabled: true
`))
	}

	return &Actor{
		app:   app,
		db:    db,
		place: place,
		sched: sched,
		daprd: daprd.New(t, dopts...),
	}
}

func (a *Actor) Run(t *testing.T, ctx context.Context) {
	a.app.Run(t, ctx)
	a.db.Run(t, ctx)
	a.place.Run(t, ctx)
	if a.sched != nil {
		a.sched.Run(t, ctx)
	}
	a.daprd.Run(t, ctx)
}

func (a *Actor) Cleanup(t *testing.T) {
	a.daprd.Cleanup(t)
	if a.sched != nil {
		a.sched.Cleanup(t)
	}
	a.place.Cleanup(t)
	a.db.Cleanup(t)
	a.app.Cleanup(t)
}

func (a *Actor) WaitUntilRunning(t *testing.T, ctx context.Context) {
	a.place.WaitUntilRunning(t, ctx)
	if a.sched != nil {
		a.sched.WaitUntilRunning(t, ctx)
	}
	a.daprd.WaitUntilRunning(t, ctx)
}

func (a *Actor) GRPCClient(t *testing.T, ctx context.Context) rtv1.DaprClient {
	t.Helper()
	return a.daprd.GRPCClient(t, ctx)
}

func (a *Actor) GRPCConn(t *testing.T, ctx context.Context) *grpc.ClientConn {
	t.Helper()
	return a.daprd.GRPCConn(t, ctx)
}

func (a *Actor) Metrics(t *testing.T, ctx context.Context) map[string]float64 {
	t.Helper()
	return a.daprd.Metrics(t, ctx)
}
