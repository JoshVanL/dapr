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
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler/cluster"
	"github.com/dapr/kit/ptr"
)

type DurableActorID struct {
	cluster *cluster.Cluster
}

func New(t *testing.T, fopts ...Option) *DurableActorID {
	t.Helper()

	opts := options{
		count: 1,
	}

	for _, fopt := range fopts {
		fopt(&opts)
	}

	return &DurableActorID{
		cluster: cluster.New(t,
			cluster.WithCount(opts.count),
		),
	}
}

func (d *DurableActorID) Run(t *testing.T, ctx context.Context) {
	t.Helper()
	d.cluster.Run(t, ctx)
}

func (d *DurableActorID) Cleanup(t *testing.T) {
	t.Helper()
	d.cluster.Cleanup(t)
}

func (d *DurableActorID) WaitUntilRunning(t *testing.T, ctx context.Context) {
	t.Helper()
	d.cluster.WaitUntilRunning(t, ctx)
}

func (d *DurableActorID) Client(t *testing.T, ctx context.Context) schedv1.SchedulerClient {
	t.Helper()
	return d.ClientN(t, ctx, 0)
}

func (d *DurableActorID) ClientN(t *testing.T, ctx context.Context, n int) schedv1.SchedulerClient {
	t.Helper()
	return d.cluster.ClientN(t, ctx, n)
}

func (d *DurableActorID) WatchJobs(t *testing.T, ctx context.Context, namespace, appID string) schedv1.Scheduler_WatchJobsClient {
	t.Helper()
	return d.WatchJobsN(t, ctx, 0, namespace, appID)
}

func (d *DurableActorID) WatchJobsN(t *testing.T, ctx context.Context, n int, namespace, appID string) schedv1.Scheduler_WatchJobsClient {
	t.Helper()

	client := d.ClientN(t, ctx, 0)

	stream, err := client.WatchJobs(ctx)
	require.NoError(t, err)

	require.NoError(t, stream.Send(&schedv1.WatchJobsRequest{
		WatchJobRequestType: &schedv1.WatchJobsRequest_Initial{
			Initial: &schedv1.WatchJobsRequestInitial{
				Namespace: namespace,
				AppId:     appID,
				AcceptJobTypes: []schedv1.JobTargetType{
					schedv1.JobTargetType_JOB_TARGET_TYPE_BROADCAST_DURABLE_ACTOR_ID,
				},
			},
		},
	}))

	return stream
}

type ScheduleOptions struct {
	Namespace string
	AppID     string
	ActorType string
	ActorID   string
	DueTime   time.Time
}

func (d *DurableActorID) Schedule(t *testing.T, ctx context.Context, opts ScheduleOptions) {
	t.Helper()

	require.NotEmpty(t, opts.Namespace, "Namespace is required")
	require.NotEmpty(t, opts.AppID, "AppID is required")
	require.NotEmpty(t, opts.ActorType, "ActorType is required")
	require.NotEmpty(t, opts.ActorID, "ActorID is required")
	require.False(t, opts.DueTime.IsZero(), "DueTime is required")

	_, err := d.Client(t, ctx).ScheduleJob(ctx, &schedv1.ScheduleJobRequest{
		Name: opts.ActorID,
		Job: &schedv1.Job{
			DueTime: ptr.Of(opts.DueTime.Format(time.RFC3339)),
		},
		Metadata: &schedv1.JobMetadata{
			AppId:     opts.AppID,
			Namespace: opts.Namespace,
			Target: &schedv1.JobTargetMetadata{
				Type: &schedv1.JobTargetMetadata_Broadcast{
					Broadcast: &schedv1.TargetBroadcast{
						Broadcast: &schedv1.TargetBroadcast_DurableActorId{
							DurableActorId: &schedv1.TargetDurableActorID{
								Type: opts.ActorType,
							},
						},
					},
				},
			},
		},
	})
	require.NoError(t, err)
}

type DeleteOptions struct {
	Namespace string
	AppID     string
	ActorType string
	ActorID   string
}

func (d *DurableActorID) Delete(t *testing.T, ctx context.Context, opts DeleteOptions) {
	t.Helper()

	require.NotEmpty(t, opts.Namespace, "Namespace is required")
	require.NotEmpty(t, opts.AppID, "AppID is required")
	require.NotEmpty(t, opts.ActorType, "ActorType is required")
	require.NotEmpty(t, opts.ActorID, "ActorID is required")

	_, err := d.Client(t, ctx).DeleteJob(ctx, &schedv1.DeleteJobRequest{
		Name: opts.ActorID,
		Metadata: &schedv1.JobMetadata{
			AppId:     opts.AppID,
			Namespace: opts.Namespace,
			Target: &schedv1.JobTargetMetadata{
				Type: &schedv1.JobTargetMetadata_Broadcast{
					Broadcast: &schedv1.TargetBroadcast{
						Broadcast: &schedv1.TargetBroadcast_DurableActorId{
							DurableActorId: &schedv1.TargetDurableActorID{
								Type: opts.ActorType,
							},
						},
					},
				},
			},
		},
	})
	require.NoError(t, err)
}

type ExpectReceiveOptions struct {
	Namespace string
	AppID     string
	ActorType string
	ActorID   string
	Data      *anypb.Any
}

func (d *DurableActorID) ExpectReceivePut(t *testing.T, ctx context.Context, stream schedv1.Scheduler_WatchJobsClient, opts ExpectReceiveOptions) {
	t.Helper()
	d.expectReceive(t, ctx, stream, schedv1.BroadcastJobEventType_BROADCAST_JOB_EVENT_PUT, opts)
}

func (d *DurableActorID) ExpectReceiveDelete(t *testing.T, ctx context.Context, stream schedv1.Scheduler_WatchJobsClient, opts ExpectReceiveOptions) {
	t.Helper()
	d.expectReceive(t, ctx, stream, schedv1.BroadcastJobEventType_BROADCAST_JOB_EVENT_DELETE, opts)
}

func (d *DurableActorID) expectReceive(t *testing.T,
	ctx context.Context,
	stream schedv1.Scheduler_WatchJobsClient,
	eventType schedv1.BroadcastJobEventType,
	opts ExpectReceiveOptions,
) {
	t.Helper()

	require.NotEmpty(t, opts.Namespace, "Namespace is required")
	require.NotEmpty(t, opts.ActorType, "ActorType is required")
	require.NotEmpty(t, opts.AppID, "AppID is required")
	require.NotEmpty(t, opts.ActorID, "ActorID is required")

	errCh := make(chan error, 1)
	gotCh := make(chan *schedv1.WatchJobsResponse, 1)

	go func() {
		g, e := stream.Recv()
		if e != nil {
			errCh <- e
			return
		}
		gotCh <- g
		errCh <- e
	}()

	var got *schedv1.WatchJobsResponse
	select {
	case <-time.After(5 * time.Second):
		require.FailNow(t, "timed out waiting for job event")
	case err := <-errCh:
		require.FailNow(t, "error receiving job event: "+err.Error())
	case got = <-gotCh:
		require.NotNil(t, got, "received nil job event")
	}
	require.NoError(t, <-errCh)

	assert.Positive(t, got.GetId())
	got.Id = 0

	data, err := anypb.New(&schedv1.BroadcastJobDataWrapper{
		Type: eventType,
		Data: opts.Data,
	})
	require.NoError(t, err)

	exp := &schedv1.WatchJobsResponse{
		Name: opts.ActorID,
		Id:   0,
		Data: data,
		Metadata: &schedv1.JobMetadata{
			AppId:     opts.AppID,
			Namespace: opts.Namespace,
			Target: &schedv1.JobTargetMetadata{
				Type: &schedv1.JobTargetMetadata_Broadcast{
					Broadcast: &schedv1.TargetBroadcast{
						Broadcast: &schedv1.TargetBroadcast_DurableActorId{
							DurableActorId: &schedv1.TargetDurableActorID{
								Type: opts.ActorType,
							},
						},
					},
				},
			},
		},
	}
	assert.True(t, proto.Equal(exp, got), "exp:%v != got:%v", exp, got)
}
