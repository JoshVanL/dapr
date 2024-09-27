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

package scheduler

import (
	"context"
	"encoding/base64"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	schedulerv1 "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(overwrite))
}

type overwrite struct {
	db        *sqlite.SQLite
	app       *app.App
	place     *placement.Placement
	scheduler *scheduler.Scheduler
}

func (o *overwrite) Setup(t *testing.T) []framework.Option {
	o.db = sqlite.New(t, sqlite.WithActorStateStore(true))
	o.app = app.New(t,
		app.WithConfig(`{"entities": ["myactortype"]}`),
		app.WithHandlerFunc("/actors/myactortype/myactorid", func(http.ResponseWriter, *http.Request) {}),
	)
	o.scheduler = scheduler.New(t)
	o.place = placement.New(t)

	return []framework.Option{
		framework.WithProcesses(o.db, o.scheduler, o.place, o.app),
	}
}

func (o *overwrite) Run(t *testing.T, ctx context.Context) {
	o.scheduler.WaitUntilRunning(t, ctx)

	opts := []daprd.Option{
		daprd.WithResourceFiles(o.db.GetComponent(t)),
		daprd.WithPlacementAddresses(o.place.Address()),
		daprd.WithSchedulerAddresses(o.scheduler.Address()),
		daprd.WithAppPort(o.app.Port()),
	}
	optsWithScheduler := append(opts,
		daprd.WithConfigManifests(t, `
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: schedulerreminders
spec:
  features:
  - name: SchedulerReminders
    enabled: true
`))

	daprd1 := daprd.New(t, optsWithScheduler...)
	daprd2 := daprd.New(t, opts...)
	daprd3 := daprd.New(t, optsWithScheduler...)

	daprd1.Run(t, ctx)
	daprd1.WaitUntilRunning(t, ctx)

	assert.Empty(t, o.db.ActorReminders(t, ctx, "myactortype").Reminders)
	assert.Empty(t, o.scheduler.EtcdJobs(t, ctx))

	_, err := daprd1.GRPCClient(t, ctx).RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
		ActorType: "myactortype",
		ActorId:   "myactorid",
		Name:      "myreminder",
		DueTime:   "10000s",
		Period:    "R100/PT10000S",
		Data:      []byte("mydata1"),
		Ttl:       "10000s",
	})
	require.NoError(t, err)

	resp := o.scheduler.ListJobActors(t, ctx, daprd1.Namespace(), daprd1.AppID(), "myactortype", "myactorid")

	require.Len(t, resp.GetJobs(), 1)
	njob := resp.GetJobs()[0]
	assert.Equal(t, "dapr/jobs/actorreminder||default||myactortype||myactorid||myreminder", njob.GetName())
	expAny, err := anypb.New(wrapperspb.Bytes([]byte(`"` + base64.URLEncoding.EncodeToString([]byte("mydata1")) + `"`)))
	require.NoError(t, err)
	assert.Equal(t, &schedulerv1.Job{
		Schedule: ptr.Of("@every 2h46m40s"),
		DueTime:  ptr.Of("10000s"),
		Ttl:      ptr.Of("10000s"),
		Data:     expAny,
		Repeats:  ptr.Of(uint32(100)),
	}, njob.GetJob())

	assert.Empty(t, o.db.ActorReminders(t, ctx, "myactortype").Reminders)
	assert.Len(t, o.scheduler.EtcdJobs(t, ctx), 1)

	daprd1.Cleanup(t)

	daprd2.Run(t, ctx)
	daprd2.WaitUntilRunning(t, ctx)
	_, err = daprd2.GRPCClient(t, ctx).RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
		ActorType: "myactortype",
		ActorId:   "myactorid",
		Name:      "myreminder",
		DueTime:   "20000s",
		Period:    "R200/PT20000S",
		Data:      []byte("mydata2"),
		Ttl:       "20000s",
	})
	require.NoError(t, err)

	assert.Len(t, o.db.ActorReminders(t, ctx, "myactortype").Reminders, 1)
	assert.Len(t, o.scheduler.EtcdJobs(t, ctx), 1)

	resp = o.scheduler.ListJobActors(t, ctx, daprd1.Namespace(), daprd1.AppID(), "myactortype", "myactorid")
	njob = resp.GetJobs()[0]
	assert.Equal(t, "dapr/jobs/actorreminder||default||myactortype||myactorid||myreminder", njob.GetName())
	expAny, err = anypb.New(wrapperspb.Bytes([]byte(`"` + base64.URLEncoding.EncodeToString([]byte("mydata1")) + `"`)))
	require.NoError(t, err)
	assert.Equal(t, &schedulerv1.Job{
		Schedule: ptr.Of("@every 2h46m40s"),
		DueTime:  ptr.Of("10000s"),
		Ttl:      ptr.Of("10000s"),
		Data:     expAny,
		Repeats:  ptr.Of(uint32(100)),
	}, njob.GetJob())

	assert.Len(t, o.db.ActorReminders(t, ctx, "myactortype").Reminders, 1)
	assert.Len(t, o.scheduler.EtcdJobs(t, ctx), 1)

	daprd2.Cleanup(t)

	daprd3.Run(t, ctx)
	daprd3.WaitUntilRunning(t, ctx)
	resp = o.scheduler.ListJobActors(t, ctx, daprd1.Namespace(), daprd1.AppID(), "myactortype", "myactorid")
	require.Len(t, resp.GetJobs(), 1)
	njob = resp.GetJobs()[0]
	assert.Equal(t, "dapr/jobs/actorreminder||default||myactortype||myactorid||myreminder", njob.GetName())
	expAny, err = anypb.New(wrapperspb.Bytes([]byte(`"` + base64.URLEncoding.EncodeToString([]byte("mydata2")) + `"`)))
	require.NoError(t, err)
	assert.Equal(t, ptr.Of("@every 5h33m20s"), njob.GetJob().Schedule)
	assert.Equal(t, ptr.Of("20000s"), njob.GetJob().DueTime)
	expTTL := time.Now().Add(20000 * time.Second)
	gotTTL, err := time.Parse(time.RFC3339, njob.GetJob().GetTtl())
	require.NoError(t, err)
	assert.InDelta(t, expTTL.UnixMilli(), gotTTL.UnixMilli(), float64(time.Second*10))
	assert.Equal(t, expAny, njob.GetJob().Data)
	assert.Equal(t, ptr.Of(uint32(200)), njob.GetJob().Repeats)

	assert.Len(t, o.db.ActorReminders(t, ctx, "myactortype").Reminders, 1)
	assert.Len(t, o.scheduler.EtcdJobs(t, ctx), 1)

	daprd3.Cleanup(t)
}
