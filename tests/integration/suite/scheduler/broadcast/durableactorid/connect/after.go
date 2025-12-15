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

package connect

import (
	"context"
	"testing"
	"time"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler/durableactorid"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(after))
}

type after struct {
	scheduler *durableactorid.DurableActorID
}

func (c *after) Setup(t *testing.T) []framework.Option {
	c.scheduler = durableactorid.New(t)

	return []framework.Option{
		framework.WithProcesses(c.scheduler),
	}
}

func (c *after) Run(t *testing.T, ctx context.Context) {
	c.scheduler.WaitUntilRunning(t, ctx)

	c.scheduler.Schedule(t, ctx, durableactorid.ScheduleOptions{
		Namespace: "namespace",
		AppID:     "appid",
		ActorType: "actortype",
		ActorID:   "actorid",
		DueTime:   time.Now().Add(time.Hour),
	})

	stream := c.scheduler.WatchJobs(t, ctx, "namespace", "appid")

	c.scheduler.ExpectReceivePut(t, ctx, stream, durableactorid.ExpectReceiveOptions{
		Namespace: "namespace",
		AppID:     "appid",
		ActorType: "actortype",
		ActorID:   "actorid",
		Data:      nil,
	})

	c.scheduler.Delete(t, ctx, durableactorid.DeleteOptions{
		Namespace: "namespace",
		AppID:     "appid",
		ActorType: "actortype",
		ActorID:   "actorid",
	})

	c.scheduler.ExpectReceiveDelete(t, ctx, stream, durableactorid.ExpectReceiveOptions{
		Namespace: "namespace",
		AppID:     "appid",
		ActorType: "actortype",
		ActorID:   "actorid",
		Data:      nil,
	})
}
