/*
Copyright 2024 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implieh.
See the License for the specific language governing permissions and
limitations under the License.
*/

package stress

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"testing"
	"time"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/anypb"
)

func init() {
	suite.Register(new(singular))
}

type singular struct {
	scheduler *scheduler.Scheduler
}

func (s *singular) Setup(t *testing.T) []framework.Option {
	s.scheduler = scheduler.New(t)
	return []framework.Option{
		framework.WithProcesses(s.scheduler),
	}
}

func (s *singular) Run(t *testing.T, ctx context.Context) {
	s.scheduler.WaitUntilRunning(t, ctx)

	client := s.scheduler.Client(t, ctx)

	jobs := 100000

	now := time.Now().UTC()
	inTenT := now.Add(80 * time.Second)
	inTen := inTenT.Format(time.RFC3339)
	fmt.Printf(">>now          %s\n", time.Now().UTC().Format(time.RFC3339))
	fmt.Printf(">>waiting till %s\n", inTen)
	for i := 0; i < jobs; i++ {
		_, err := client.ScheduleJob(ctx, &schedulerv1pb.ScheduleJobRequest{
			Name: "job-" + strconv.Itoa(i),
			Job: &schedulerv1pb.Job{
				Data:    &anypb.Any{Value: []byte(strconv.Itoa(i))},
				DueTime: &inTen,
			},
			Metadata: &schedulerv1pb.ScheduleJobMetadata{
				AppId:     "app1",
				Namespace: "default",
				Type: &schedulerv1pb.ScheduleJobMetadataType{
					Type: &schedulerv1pb.ScheduleJobMetadataType_Job{
						Job: new(schedulerv1pb.ScheduleTypeJob),
					},
				},
			},
		})
		require.NoError(t, err)
		if (i+1)%1000 == 0 {
			fmt.Printf("^^ %d %s\n", i+1, inTenT.Sub(time.Now().UTC()))
		}
	}

	writeTaken := time.Since(now)

	fmt.Printf(">>write took   %s\n", writeTaken)
	fmt.Printf(">>now          %s\n", time.Now().UTC().Format(time.RFC3339))
	fmt.Printf(">>waiting till %s (%s)\n", inTen, inTenT.Sub(time.Now()))
	stream, err := client.WatchJobs(ctx)
	require.NoError(t, err)

	require.NoError(t, stream.Send(&schedulerv1pb.WatchJobsRequest{
		WatchJobRequestType: &schedulerv1pb.WatchJobsRequest_Initial{
			Initial: &schedulerv1pb.WatchJobsRequestInitial{
				AppId:     "app1",
				Namespace: "default",
			},
		},
	}))

	got := make([]*anypb.Any, jobs)
	g, err := stream.Recv()
	require.NoError(t, err)
	require.NoError(t, stream.Send(&schedulerv1pb.WatchJobsRequest{
		WatchJobRequestType: &schedulerv1pb.WatchJobsRequest_Result{
			Result: &schedulerv1pb.WatchJobsRequestResult{
				Uuid: g.Uuid,
			},
		},
	}))
	now = time.Now().UTC()
	fmt.Printf(">>got first    %s\n", now.Format(time.RFC3339))
	got[0] = g.Data

	ii := make(chan uint64, jobs)
	go func() {
		for ix := 1; ix < jobs; ix++ {
			f := <-ii
			require.NoError(t, stream.Send(&schedulerv1pb.WatchJobsRequest{
				WatchJobRequestType: &schedulerv1pb.WatchJobsRequest_Result{
					Result: &schedulerv1pb.WatchJobsRequestResult{
						Uuid: f,
					},
				},
			}))
		}
	}()

	for i := 1; i < jobs; i++ {
		g, err := stream.Recv()
		require.NoError(t, err)
		got[i] = g.Data
		ii <- g.Uuid
		if (i+1)%1000 == 0 {
			fmt.Printf(">>%d\n", i+1)
		}
	}
	triggerTaken := time.Since(now)
	fmt.Printf("write took   %s %.4fqps (%d/%s)\n", writeTaken, float64(jobs)/writeTaken.Seconds(), jobs, writeTaken)
	fmt.Printf("trigger took %s %.4fqps (%d/%s)\n", triggerTaken, float64(jobs)/triggerTaken.Seconds(), jobs, triggerTaken)

	gotI := make([]int, jobs)
	for i := 0; i < jobs; i++ {
		gotI[i], err = strconv.Atoi(string(got[i].Value))
		require.NoError(t, err)
	}
	sort.Ints(gotI)
	for i := 0; i < jobs; i++ {
		require.Equal(t, i, gotI[i])
	}
}
