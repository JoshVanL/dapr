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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"

	schedv1 "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(base))
}

type base struct {
	scheduler *scheduler.Scheduler
}

func (b *base) Setup(t *testing.T) []framework.Option {
	b.scheduler = scheduler.New(t)

	return []framework.Option{
		framework.WithProcesses(b.scheduler),
	}
}

func (b *base) Run(t *testing.T, ctx context.Context) {
	b.scheduler.WaitUntilRunning(t, ctx)

	client := b.scheduler.Client(t, ctx)

	watch, err := client.WatchJobs(ctx)
	require.NoError(t, err)

	require.NoError(t, watch.Send(&schedv1.WatchJobsRequest{
		WatchJobRequestType: &schedv1.WatchJobsRequest_Initial{
			Initial: &schedv1.WatchJobsRequestInitial{
				Namespace: "namespace",
				AppId:     "appid",
				AcceptJobTypes: []schedv1.JobTargetType{
					schedv1.JobTargetType_JOB_TARGET_TYPE_BROADCAST_DURABLE_ACTOR_ID,
				},
			},
		},
	}))

	// TODO: @joshvanl: validate all string fields cannot be empty.
	_, err = client.ScheduleJob(ctx, &schedv1.ScheduleJobRequest{
		Name: "actorid",
		Job: &schedv1.Job{
			DueTime: ptr.Of(time.Now().Add(time.Hour).Format(time.RFC3339)),
		},
		Metadata: &schedv1.JobMetadata{
			AppId:     "appid",
			Namespace: "namespace",
			Target: &schedv1.JobTargetMetadata{
				Type: &schedv1.JobTargetMetadata_Broadcast{
					Broadcast: &schedv1.TargetBroadcast{
						Broadcast: &schedv1.TargetBroadcast_DurableActorId{
							DurableActorId: &schedv1.TargetDurableActorID{
								Type: "actortype",
							},
						},
					},
				},
			},
		},
	})
	require.NoError(t, err)

	recv, err := watch.Recv()
	require.NoError(t, err)
	assert.Positive(t, recv.GetId())
	recv.Id = 0

	data, err := anypb.New(&schedv1.BroadcastJobDataWrapper{
		Type: schedv1.BroadcastJobEventType_BROADCAST_JOB_EVENT_PUT,
		Data: nil,
	})
	require.NoError(t, err)

	exp := &schedv1.WatchJobsResponse{
		Name: "actorid",
		Id:   0,
		Data: data,
		Metadata: &schedv1.JobMetadata{
			AppId:     "appid",
			Namespace: "namespace",
			Target: &schedv1.JobTargetMetadata{
				Type: &schedv1.JobTargetMetadata_Broadcast{
					Broadcast: &schedv1.TargetBroadcast{
						Broadcast: &schedv1.TargetBroadcast_DurableActorId{
							DurableActorId: &schedv1.TargetDurableActorID{
								Type: "actortype",
							},
						},
					},
				},
			},
		},
	}
	assert.True(t, proto.Equal(exp, recv), "%v != %v", exp, recv)

	_, err = client.DeleteJob(ctx, &schedv1.DeleteJobRequest{
		Name: "actorid",
		Metadata: &schedv1.JobMetadata{
			AppId:     "appid",
			Namespace: "namespace",
			Target: &schedv1.JobTargetMetadata{
				Type: &schedv1.JobTargetMetadata_Broadcast{
					Broadcast: &schedv1.TargetBroadcast{
						Broadcast: &schedv1.TargetBroadcast_DurableActorId{
							DurableActorId: &schedv1.TargetDurableActorID{
								Type: "actortype",
							},
						},
					},
				},
			},
		},
	})
	require.NoError(t, err)

	recv, err = watch.Recv()
	require.NoError(t, err)
	assert.Positive(t, recv.GetId())
	recv.Id = 0
	data, err = anypb.New(&schedv1.BroadcastJobDataWrapper{
		Type: schedv1.BroadcastJobEventType_BROADCAST_JOB_EVENT_DELETE,
		Data: nil,
	})
	require.NoError(t, err)
	exp = &schedv1.WatchJobsResponse{
		Name: "actorid",
		Id:   0,
		Data: data,
		Metadata: &schedv1.JobMetadata{
			AppId:     "appid",
			Namespace: "namespace",
			Target: &schedv1.JobTargetMetadata{
				Type: &schedv1.JobTargetMetadata_Broadcast{
					Broadcast: &schedv1.TargetBroadcast{
						Broadcast: &schedv1.TargetBroadcast_DurableActorId{
							DurableActorId: &schedv1.TargetDurableActorID{
								Type: "actortype",
							},
						},
					},
				},
			},
		},
	}
	assert.True(t, proto.Equal(exp, recv), "%v != %v", exp, recv)
}
