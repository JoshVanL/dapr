/*
Copyright 2025 The Dapr Authors
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

package duetime

import (
	"context"
	"testing"
	"time"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler/durableactorid"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(order))
}

type order struct {
	scheduler *durableactorid.DurableActorID
}

func (d *order) Setup(t *testing.T) []framework.Option {
	d.scheduler = durableactorid.New(t)

	return []framework.Option{
		framework.WithProcesses(d.scheduler),
	}
}

func (d *order) Run(t *testing.T, ctx context.Context) {
	d.scheduler.WaitUntilRunning(t, ctx)

	stream := d.scheduler.WatchJobs(t, ctx, "my-namespace", "my-appid")

	sopts := durableactorid.ScheduleOptions{
		Namespace: "my-namespace",
		AppID:     "my-appid",
		ActorType: "my-actortype",
		ActorID:   "my-actorid1",
		DueTime:   time.Now(),
	}
	d.scheduler.Schedule(t, ctx, sopts)

	sopts.ActorID = "my-actorid2"
	d.scheduler.Schedule(t, ctx, sopts)

	sopts.ActorID = "my-actorid3"
	d.scheduler.Schedule(t, ctx, sopts)

	opts := durableactorid.ExpectReceiveOptions{
		Namespace: "my-namespace",
		AppID:     "my-appid",
		ActorType: "my-actortype",
		ActorID:   "my-actorid1",
		Data:      nil,
	}
	d.scheduler.ExpectReceivePut(t, ctx, stream, opts)
	d.scheduler.ExpectReceiveDelete(t, ctx, stream, opts)

	opts.ActorID = "my-actorid2"
	d.scheduler.ExpectReceivePut(t, ctx, stream, opts)
	d.scheduler.ExpectReceiveDelete(t, ctx, stream, opts)

	opts.ActorID = "my-actorid3"
	d.scheduler.ExpectReceivePut(t, ctx, stream, opts)
	d.scheduler.ExpectReceiveDelete(t, ctx, stream, opts)
}
