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

package jobs

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/app"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(inflight))
}

type inflight struct {
	daprd     *daprd.Daprd
	scheduler *scheduler.Scheduler
	jobChan   chan *runtimev1pb.JobEventRequest
	releaseCh chan struct{}
}

func (i *inflight) Setup(t *testing.T) []framework.Option {
	i.scheduler = scheduler.New(t)

	i.jobChan = make(chan *runtimev1pb.JobEventRequest)
	i.releaseCh = make(chan struct{})
	srv := app.New(t,
		app.WithOnJobEventFn(func(ctx context.Context, in *runtimev1pb.JobEventRequest) (*runtimev1pb.JobEventResponse, error) {
			fmt.Printf(">>IN TRIGGER: %v\n", in)
			i.jobChan <- in
			<-i.releaseCh
			return new(runtimev1pb.JobEventResponse), nil
		}),
	)

	i.daprd = daprd.New(t,
		daprd.WithSchedulerAddresses(i.scheduler.Address()),
		daprd.WithAppPort(srv.Port(t)),
		daprd.WithAppProtocol("grpc"),
	)

	return []framework.Option{
		framework.WithProcesses(i.scheduler, srv, i.daprd),
	}
}

func (i *inflight) Run(t *testing.T, ctx context.Context) {
	i.scheduler.WaitUntilRunning(t, ctx)
	i.daprd.WaitUntilRunning(t, ctx)

	client := i.daprd.GRPCClient(t, ctx)

	const numJobs = 100
	payloads := make([]*anypb.Any, numJobs)

	var err error
	for xy := range payloads {
		payloads[xy], err = anypb.New(wrapperspb.String(strconv.Itoa(xy)))
		require.NoError(t, err)
	}

	_, err = client.ScheduleJobAlpha1(ctx, &runtimev1pb.ScheduleJobRequest{
		Job: &runtimev1pb.Job{
			Name:    "test",
			DueTime: ptr.Of("0s"),
			Data:    payloads[0],
		},
	})
	require.NoError(t, err)

	select {
	case <-time.After(time.Second * 3):
		require.Fail(t, "timed out waiting for triggered job")
	case job := <-i.jobChan:
		assert.True(t, proto.Equal(payloads[0], job.GetData()), "%v != %v", payloads[0], job.GetData())
	}

	for xy := 1; xy < numJobs; xy++ {
		_, err = client.ScheduleJobAlpha1(ctx, &runtimev1pb.ScheduleJobRequest{
			Job: &runtimev1pb.Job{
				Name:    "test",
				DueTime: ptr.Of("0s"),
				Data:    payloads[xy],
			},
		})
		require.NoError(t, err)
	}

	close(i.releaseCh)

	for xy := 1; xy < numJobs; xy++ {
		select {
		case <-time.After(time.Second * 3):
			require.Fail(t, "timed out waiting for triggered job")
		case job := <-i.jobChan:
			assert.True(t, proto.Equal(payloads[xy], job.GetData()), "%v != %v", payloads[xy], job.GetData())
		}
	}
}
