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

package api

import (
	"context"
	"strconv"
	"testing"
	"time"

	schedulerv1 "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/ptr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	suite.Register(new(list))
}

type list struct {
	scheduler *scheduler.Scheduler
}

func (l *list) Setup(t *testing.T) []framework.Option {
	l.scheduler = scheduler.New(t)

	return []framework.Option{
		framework.WithProcesses(l.scheduler),
	}
}

func (l *list) Run(t *testing.T, ctx context.Context) {
	l.scheduler.WaitUntilRunning(t, ctx)

	// TODO: @joshvanl: error codes on List API.

	jobMetadata := func(ns, id string) *schedulerv1pb.JobMetadata {
		return &schedulerv1pb.JobMetadata{
			Namespace: ns, AppId: id,
			Target: &schedulerv1pb.JobTargetMetadata{
				Type: &schedulerv1pb.JobTargetMetadata_Job{
					Job: new(schedulerv1pb.TargetJob),
				},
			},
		}
	}

	actorMetadata := func(ns, id, actorType, actorID string) *schedulerv1pb.JobMetadata {
		return &schedulerv1pb.JobMetadata{
			Namespace: ns, AppId: "test",
			Target: &schedulerv1pb.JobTargetMetadata{
				Type: &schedulerv1pb.JobTargetMetadata_Actor{
					Actor: &schedulerv1pb.TargetActorReminder{
						Type: actorType, Id: actorID,
					},
				},
			},
		}
	}

	jobRequest := func(ns, id string) *schedulerv1pb.ListJobsRequest {
		return &schedulerv1pb.ListJobsRequest{
			Metadata: jobMetadata(ns, id),
		}
	}

	actorRequest := func(ns, id, actorType, actorID string) *schedulerv1pb.ListJobsRequest {
		return &schedulerv1pb.ListJobsRequest{
			Metadata: actorMetadata(ns, id, actorType, actorID),
		}
	}

	client := l.scheduler.Client(t, ctx)
	assertLen := func(t *testing.T, req *schedulerv1pb.ListJobsRequest, expected int) {
		t.Helper()
		resp, err := client.ListJobs(ctx, req)
		require.NoError(t, err)
		assert.Len(t, resp.GetJobs(), expected)
	}

	resp, err := client.ListJobs(ctx, jobRequest("default", "test"))
	require.NoError(t, err)
	assert.False(t, resp.GetHasMore())
	assert.Empty(t, resp.GetJobs())
	resp, err = client.ListJobs(ctx, actorRequest("default", "test", "myactortype", "myactorid"))
	require.NoError(t, err)
	assert.False(t, resp.GetHasMore())
	assert.Empty(t, resp.GetJobs())

	_, err = client.ScheduleJob(ctx, &schedulerv1.ScheduleJobRequest{
		Name:     "test",
		Job:      &schedulerv1.Job{Schedule: ptr.Of("@every 20s")},
		Metadata: jobMetadata("default", "test"),
	})
	require.NoError(t, err)

	assertLen(t, jobRequest("default", "test"), 1)
	assertLen(t, jobRequest("default", "not-test"), 0)
	assertLen(t, jobRequest("not-default", "test"), 0)
	assertLen(t, actorRequest("default", "test", "myactortype", "myactorid"), 0)

	for i := 0; i < 10; i++ {
		_, err = client.ScheduleJob(ctx, &schedulerv1.ScheduleJobRequest{
			Name:     strconv.Itoa(i),
			Job:      &schedulerv1.Job{Schedule: ptr.Of("@every 20s")},
			Metadata: jobMetadata("default", "test"),
		})
	}
	assertLen(t, jobRequest("default", "test"), 11)
	assertLen(t, jobRequest("default", "not-test"), 0)
	assertLen(t, jobRequest("not-default", "test"), 0)

	_, err = client.ScheduleJob(ctx, &schedulerv1.ScheduleJobRequest{
		Name: "test",
		Job:  &schedulerv1.Job{Schedule: ptr.Of("@every 20s")},
		Metadata: &schedulerv1.JobMetadata{
			Namespace: "not-default",
			AppId:     "test",
			Target: &schedulerv1.JobTargetMetadata{
				Type: new(schedulerv1.JobTargetMetadata_Job),
			},
		},
	})
	assertLen(t, jobRequest("default", "test"), 11)
	assertLen(t, jobRequest("default", "not-test"), 0)
	assertLen(t, jobRequest("not-default", "test"), 1)

	_, err = client.ScheduleJob(ctx, &schedulerv1.ScheduleJobRequest{
		Name:     "test",
		Job:      &schedulerv1.Job{Schedule: ptr.Of("@every 20s")},
		Metadata: jobMetadata("default", "not-test"),
	})
	assertLen(t, jobRequest("default", "test"), 11)
	assertLen(t, jobRequest("default", "not-test"), 1)
	assertLen(t, jobRequest("not-default", "test"), 1)

	_, err = client.ScheduleJob(ctx, &schedulerv1.ScheduleJobRequest{
		Name:     "test",
		Job:      &schedulerv1.Job{Schedule: ptr.Of("@every 20s")},
		Metadata: actorMetadata("default", "test", "myactortype", "myactorid"),
	})
	assertLen(t, jobRequest("default", "test"), 11)
	assertLen(t, jobRequest("default", "not-test"), 1)
	assertLen(t, jobRequest("not-default", "test"), 1)
	assertLen(t, actorRequest("default", "test", "myactortype", "myactorid"), 1)
	assertLen(t, actorRequest("not-default", "test", "myactortype", "myactorid"), 0)
	assertLen(t, actorRequest("default", "test", "myactortype", "not-myactorid"), 0)
	assertLen(t, actorRequest("default", "test", "not-myactortype", "myactorid"), 0)
	assertLen(t, actorRequest("default", "test", "not-myactortype", "not-myactorid"), 0)

	_, err = client.ScheduleJob(ctx, &schedulerv1.ScheduleJobRequest{
		Name:     "test",
		Job:      &schedulerv1.Job{Schedule: ptr.Of("@every 20s")},
		Metadata: actorMetadata("default", "test", "not-myactortype", "myactorid"),
	})
	assertLen(t, jobRequest("default", "test"), 11)
	assertLen(t, jobRequest("default", "not-test"), 1)
	assertLen(t, jobRequest("not-default", "test"), 1)
	assertLen(t, actorRequest("default", "test", "myactortype", "myactorid"), 1)
	assertLen(t, actorRequest("not-default", "test", "myactortype", "myactorid"), 0)
	assertLen(t, actorRequest("default", "test", "myactortype", "not-myactorid"), 0)
	assertLen(t, actorRequest("default", "test", "not-myactortype", "myactorid"), 1)
	assertLen(t, actorRequest("default", "test", "not-myactortype", "not-myactorid"), 0)

	_, err = client.ScheduleJob(ctx, &schedulerv1.ScheduleJobRequest{
		Name:     "test",
		Job:      &schedulerv1.Job{Schedule: ptr.Of("@every 20s")},
		Metadata: actorMetadata("default", "test", "myactortype", "not-myactorid"),
	})
	assertLen(t, jobRequest("default", "test"), 11)
	assertLen(t, jobRequest("default", "not-test"), 1)
	assertLen(t, jobRequest("not-default", "test"), 1)
	assertLen(t, actorRequest("default", "test", "myactortype", "myactorid"), 1)
	assertLen(t, actorRequest("not-default", "test", "not-myactortype", "myactorid"), 0)
	assertLen(t, actorRequest("default", "test", "not-myactortype", "myactorid"), 1)
	assertLen(t, actorRequest("default", "test", "myactortype", "not-myactorid"), 1)
	assertLen(t, actorRequest("default", "test", "not-myactortype", "not-myactorid"), 0)

	_, err = client.ScheduleJob(ctx, &schedulerv1.ScheduleJobRequest{
		Name:     "test2",
		Job:      &schedulerv1.Job{Schedule: ptr.Of("@every 20s")},
		Metadata: actorMetadata("default", "test", "myactortype", "not-myactorid"),
	})
	assertLen(t, jobRequest("default", "test"), 11)
	assertLen(t, jobRequest("default", "not-test"), 1)
	assertLen(t, jobRequest("not-default", "test"), 1)
	assertLen(t, actorRequest("default", "test", "myactortype", "myactorid"), 1)
	assertLen(t, actorRequest("not-default", "test", "not-myactortype", "myactorid"), 0)
	assertLen(t, actorRequest("default", "test", "not-myactortype", "myactorid"), 1)
	assertLen(t, actorRequest("default", "test", "myactortype", "not-myactorid"), 2)
	assertLen(t, actorRequest("default", "test", "not-myactortype", "not-myactorid"), 0)

	_, err = client.DeleteJob(ctx, &schedulerv1.DeleteJobRequest{
		Name:     "test",
		Metadata: jobMetadata("default", "test"),
	})
	assertLen(t, jobRequest("default", "test"), 10)
	assertLen(t, jobRequest("default", "not-test"), 1)
	assertLen(t, jobRequest("not-default", "test"), 1)
	assertLen(t, actorRequest("default", "test", "myactortype", "myactorid"), 1)
	assertLen(t, actorRequest("not-default", "test", "not-myactortype", "myactorid"), 0)
	assertLen(t, actorRequest("default", "test", "not-myactortype", "myactorid"), 1)
	assertLen(t, actorRequest("default", "test", "myactortype", "not-myactorid"), 2)
	assertLen(t, actorRequest("default", "test", "not-myactortype", "not-myactorid"), 0)

	for i := 0; i < 6; i++ {
		_, err = client.DeleteJob(ctx, &schedulerv1.DeleteJobRequest{
			Name:     strconv.Itoa(i),
			Metadata: jobMetadata("default", "test"),
		})
	}
	assertLen(t, jobRequest("default", "test"), 4)
	assertLen(t, jobRequest("default", "not-test"), 1)
	assertLen(t, jobRequest("not-default", "test"), 1)
	assertLen(t, actorRequest("default", "test", "myactortype", "myactorid"), 1)
	assertLen(t, actorRequest("not-default", "test", "not-myactortype", "myactorid"), 0)
	assertLen(t, actorRequest("default", "test", "not-myactortype", "myactorid"), 1)
	assertLen(t, actorRequest("default", "test", "myactortype", "not-myactorid"), 2)
	assertLen(t, actorRequest("default", "test", "not-myactortype", "not-myactorid"), 0)

	_, err = client.ScheduleJob(ctx, &schedulerv1.ScheduleJobRequest{
		Name:     "test123",
		Job:      &schedulerv1.Job{DueTime: ptr.Of(time.Now().Add(time.Second * 2).Format(time.RFC3339))},
		Metadata: jobMetadata("default", "test"),
	})
	require.NoError(t, err)
	assertLen(t, jobRequest("default", "test"), 5)
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		resp, err := client.ListJobs(ctx, jobRequest("default", "test"))
		require.NoError(t, err)
		assert.Len(c, resp.GetJobs(), 4)
	}, time.Second*10, time.Millisecond*10)
}
