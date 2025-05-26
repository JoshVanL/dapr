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

package broadcast

import (
	"context"
	"testing"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/ptr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func init() {
	suite.Register(new(basic))
}

type basic struct {
	scheduler *scheduler.Scheduler
}

func (b *basic) Setup(t *testing.T) []framework.Option {
	b.scheduler = scheduler.New(t)

	return []framework.Option{
		framework.WithProcesses(b.scheduler),
	}
}

func (b *basic) Run(t *testing.T, ctx context.Context) {
	b.scheduler.WaitUntilRunning(t, ctx)

	client := b.scheduler.Client(t, ctx)
	stream, err := client.WatchJobs(ctx)
	require.NoError(t, err)

	require.NoError(t, stream.Send(&schedulerv1pb.WatchJobsRequest{
		WatchJobRequestType: &schedulerv1pb.WatchJobsRequest_Initial{
			Initial: &schedulerv1pb.WatchJobsRequestInitial{
				AppId:      "app1",
				Namespace:  "ns1",
				ActorTypes: []string{"actor1", "actor2"},
				AcceptJobTypes: []schedulerv1pb.JobTargetType{
					schedulerv1pb.JobTargetType_JOB_TARGET_TYPE_JOB,
					schedulerv1pb.JobTargetType_JOB_TARGET_TYPE_ACTOR_REMINDER,
				},
			},
		},
	}))

	data, err := anypb.New(wrapperspb.String("data"))
	require.NoError(t, err)

	meta := &schedulerv1pb.JobMetadata{
		AppId:     "app1",
		Namespace: "ns1",
		Target: &schedulerv1pb.JobTargetMetadata{
			Type: &schedulerv1pb.JobTargetMetadata_Broadcast{
				Broadcast: &schedulerv1pb.TargetBroadcast{
					Broadcast: &schedulerv1pb.TargetBroadcast_DurableActorId{
						DurableActorId: &schedulerv1pb.TargetDurableActorID{
							Id:   "id1",
							Type: "actor1",
						},
					},
				},
			},
		},
	}

	_, err = client.ScheduleJob(ctx, &schedulerv1pb.ScheduleJobRequest{
		Name: "foo",
		Job: &schedulerv1pb.Job{
			Schedule: ptr.Of("@every 1s"),
			Repeats:  ptr.Of(uint32(2)),
			Data:     data,
		},
		Metadata: meta,
	})
	require.NoError(t, err)

	datawrapPUT, err := anypb.New(&schedulerv1pb.BroadcastJobDataWrapper{
		Type: schedulerv1pb.BroadcastJobEventType_BROADCAST_JOB_EVENT_PUT,
		Data: data,
	})
	require.NoError(t, err)
	datawrapTRIGGER, err := anypb.New(&schedulerv1pb.BroadcastJobDataWrapper{
		Type: schedulerv1pb.BroadcastJobEventType_BROADCAST_JOB_EVENT_TRIGGER,
		Data: data,
	})
	require.NoError(t, err)
	datawrapDELETE, err := anypb.New(&schedulerv1pb.BroadcastJobDataWrapper{
		Type: schedulerv1pb.BroadcastJobEventType_BROADCAST_JOB_EVENT_DELETE,
	})
	require.NoError(t, err)

	exp := &schedulerv1pb.WatchJobsResponse{
		Name:     "foo",
		Id:       1,
		Data:     datawrapPUT,
		Metadata: meta,
	}

	resp, err := stream.Recv()
	require.NoError(t, err)
	assert.True(t, proto.Equal(exp, resp), "%v != %v", exp, resp)

	require.NoError(t, stream.Send(&schedulerv1pb.WatchJobsRequest{
		WatchJobRequestType: &schedulerv1pb.WatchJobsRequest_Result{
			Result: &schedulerv1pb.WatchJobsRequestResult{
				Id:     1,
				Status: schedulerv1pb.WatchJobsRequestResultStatus_SUCCESS,
			},
		},
	}))

	resp, err = stream.Recv()
	require.NoError(t, err)
	exp.Id = 2
	exp.Data = datawrapTRIGGER
	assert.True(t, proto.Equal(exp, resp), "%v != %v", exp, resp)
	require.NoError(t, stream.Send(&schedulerv1pb.WatchJobsRequest{
		WatchJobRequestType: &schedulerv1pb.WatchJobsRequest_Result{
			Result: &schedulerv1pb.WatchJobsRequestResult{
				Id:     exp.Id,
				Status: schedulerv1pb.WatchJobsRequestResultStatus_SUCCESS,
			},
		},
	}))

	resp, err = stream.Recv()
	require.NoError(t, err)
	exp.Id = 3
	assert.True(t, proto.Equal(exp, resp), "%v != %v", exp, resp)
	require.NoError(t, stream.Send(&schedulerv1pb.WatchJobsRequest{
		WatchJobRequestType: &schedulerv1pb.WatchJobsRequest_Result{
			Result: &schedulerv1pb.WatchJobsRequestResult{
				Id:     exp.Id,
				Status: schedulerv1pb.WatchJobsRequestResultStatus_SUCCESS,
			},
		},
	}))

	exp = &schedulerv1pb.WatchJobsResponse{
		Name:     "foo",
		Id:       4,
		Data:     datawrapDELETE,
		Metadata: meta,
	}
	resp, err = stream.Recv()
	require.NoError(t, err)
	assert.True(t, proto.Equal(exp, resp), "%v != %v", exp, resp)
}
