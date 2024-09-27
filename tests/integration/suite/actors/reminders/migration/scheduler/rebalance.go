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
	"strconv"
	"testing"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	suite.Register(new(rebalance))
}

type rebalance struct {
	db *sqlite.SQLite

	actor1 *actors.Actors
	actor2 *actors.Actors
}

func (r *rebalance) Setup(t *testing.T) []framework.Option {
	r.db = sqlite.New(t,
		sqlite.WithActorStateStore(true),
		sqlite.WithCreateStateTables(),
	)
	r.actor1 = actors.New(t,
		actors.WithDB(r.db),
		actors.WithActorTypes("myactortype"),
	)
	r.actor2 = actors.New(t,
		actors.WithScheduler(r.actor1.Scheduler()),
		actors.WithDB(r.db),
		actors.WithActorTypes("myactortype"),
		actors.WithPlacement(r.actor1.Placement()),
		actors.WithFeatureSchedulerReminders(true),
	)

	return []framework.Option{
		framework.WithProcesses(r.db, r.actor1, r.actor2),
	}
}

func (r *rebalance) Run(t *testing.T, ctx context.Context) {
	r.actor1.WaitUntilRunning(t, ctx)
	r.actor2.WaitUntilRunning(t, ctx)

	assert.Empty(t, r.actor1.DB().ActorReminders(t, ctx, "myactortype").Reminders)
	assert.Empty(t, r.actor2.DB().ActorReminders(t, ctx, "myactortype").Reminders)
	assert.Empty(t, r.actor1.Scheduler().EtcdJobs(t, ctx))
	assert.Empty(t, r.actor2.Scheduler().EtcdJobs(t, ctx))

	for i := range 200 {
		_, err := r.actor1.GRPCClient(t, ctx).RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
			ActorType: "myactortype",
			ActorId:   "initial-" + strconv.Itoa(i),
			Name:      "foo",
			DueTime:   "1000s",
		})
		require.NoError(t, err)
	}

	assert.Len(t, r.actor1.DB().ActorReminders(t, ctx, "myactortype").Reminders, 200)
	assert.Len(t, r.actor2.DB().ActorReminders(t, ctx, "myactortype").Reminders, 200)
	assert.Empty(t, r.actor1.Scheduler().EtcdJobs(t, ctx))
	assert.Empty(t, r.actor2.Scheduler().EtcdJobs(t, ctx))

	t.Run("new daprd", func(t *testing.T) {
		daprd := actors.New(t,
			actors.WithDB(r.db),
			actors.WithActorTypes("myactortype"),
			actors.WithFeatureSchedulerReminders(false),
			actors.WithPlacement(r.actor1.Placement()),
			actors.WithScheduler(r.actor1.Scheduler()),
		)
		daprd.Run(t, ctx)
		daprd.WaitUntilRunning(t, ctx)

		assert.Len(t, r.actor1.DB().ActorReminders(t, ctx, "myactortype").Reminders, 200)
		assert.Len(t, r.actor2.DB().ActorReminders(t, ctx, "myactortype").Reminders, 200)
		assert.NotEmpty(t, r.actor1.Scheduler().EtcdJobs(t, ctx))
		assert.NotEmpty(t, r.actor2.Scheduler().EtcdJobs(t, ctx))
		daprd.Cleanup(t)
	})
}
