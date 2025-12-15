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

package durableactorid

import (
	"context"
	"testing"
	"time"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler/durableactorid"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/stretchr/testify/require"
)

func init() {
	suite.Register(new(reconnect))
}

type reconnect struct {
	scheduler *durableactorid.DurableActorID
}

func (r *reconnect) Setup(t *testing.T) []framework.Option {
	r.scheduler = durableactorid.New(t)

	return []framework.Option{
		framework.WithProcesses(r.scheduler),
	}
}

func (r *reconnect) Run(t *testing.T, ctx context.Context) {
	r.scheduler.WaitUntilRunning(t, ctx)

	stream := r.scheduler.WatchJobs(t, ctx, "namespace", "appid")

	r.scheduler.Schedule(t, ctx, durableactorid.ScheduleOptions{
		Namespace: "namespace",
		AppID:     "appid",
		ActorType: "actortype",
		ActorID:   "actorid",
		DueTime:   time.Now().Add(time.Hour),
	})

	r.scheduler.ExpectReceivePut(t, ctx, stream, durableactorid.ExpectReceiveOptions{
		Namespace: "namespace",
		AppID:     "appid",
		ActorType: "actortype",
		ActorID:   "actorid",
		Data:      nil,
	})

	require.NoError(t, stream.CloseSend())

	stream = r.scheduler.WatchJobs(t, ctx, "namespace", "appid")

	r.scheduler.ExpectReceivePut(t, ctx, stream, durableactorid.ExpectReceiveOptions{
		Namespace: "namespace",
		AppID:     "appid",
		ActorType: "actortype",
		ActorID:   "actorid",
		Data:      nil,
	})

	r.scheduler.Delete(t, ctx, durableactorid.DeleteOptions{
		Namespace: "namespace",
		AppID:     "appid",
		ActorType: "actortype",
		ActorID:   "actorid",
	})

	r.scheduler.ExpectReceiveDelete(t, ctx, stream, durableactorid.ExpectReceiveOptions{
		Namespace: "namespace",
		AppID:     "appid",
		ActorType: "actortype",
		ActorID:   "actorid",
		Data:      nil,
	})
}
