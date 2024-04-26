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
	"sync"
	"sync/atomic"
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

	const num = 100000

	now := time.Now().UTC()
	inTenT := now.Add(30 * time.Second)
	inTen := inTenT.Format(time.RFC3339)
	fmt.Printf(">>now          %s\n", time.Now().UTC().Format(time.RFC3339))
	fmt.Printf(">>waiting till %s\n", inTen)

	wrk := func(i int) {
		_, err := client.ScheduleJob(ctx, &schedulerv1pb.ScheduleJobRequest{
			Name: "job-" + strconv.Itoa(i),
			Job: &schedulerv1pb.Job{
				Data:    &anypb.Any{Value: []byte(strconv.Itoa(i))},
				DueTime: &inTen, //REFACTOR
				//Schedule: fmt.Sprintf("%d %d * * *", inTenT.Second(), inTenT.Minute()), //OLD
				//Schedule: inTen,
				//Repeats: ptr.Of(uint32(1)),
			},
			Metadata: &schedulerv1pb.ScheduleJobMetadata{
				AppId:     "app1",
				Namespace: "default",
				Type: &schedulerv1pb.ScheduleJobMetadataType{
					Source: &schedulerv1pb.ScheduleJobMetadataType_App{
						App: new(schedulerv1pb.ScheduleJobMetadataSourceApp),
					},
				},
			},
		})
		require.NoError(t, err)
		if (i+1)%1000 == 0 {
			fmt.Printf("^^ %05d %s\n", i+1, inTenT.Sub(time.Now().UTC()))
		}
	}

	var ii atomic.Int64

	var wg sync.WaitGroup
	wg.Add(20)
	for i := 0; i < 20; i++ {
		go func() {
			defer wg.Done()
			for {
				ik := ii.Add(1) - 1
				if ik >= num {
					return
				}
				wrk(int(ik))
			}
		}()
	}

	wg.Wait()

	//for i := 0; i < num; i++ {
	//	wrk(i)
	//}

	//		for i := 0; i < num; i++ {
	//		_, err := client.ScheduleJob(ctx, &schedulerv1pb.ScheduleJobRequest{
	//			Job: &runtimev1pb.Job{
	//				Name:    "job-" + strconv.Itoa(i),
	//				Data:    &anypb.Any{Value: []byte(strconv.Itoa(i))},
	//				DueTime: &inTen, //REFACTOR
	//				//Schedule: fmt.Sprintf("%d %d * * *", inTenT.Second(), inTenT.Minute()), //OLD
	//			},
	//			AppId:     "app1",
	//			Namespace: "default",
	//		})
	//		require.NoError(t, err)
	//		if (i+1)%1000 == 0 {
	//			fmt.Printf("^^ %d %s\n", i+1, inTenT.Sub(time.Now().UTC()))
	//		}
	//	}

	writeTaken := time.Since(now)

	fmt.Printf(">>write took   %s\n", writeTaken)
	fmt.Printf(">>now          %s\n", time.Now().UTC().Format(time.RFC3339))
	fmt.Printf(">>waiting till %s (%s)\n", inTen, inTenT.Sub(time.Now()))

	stream, err := client.WatchJobs(ctx, &schedulerv1pb.WatchJobsRequest{
		AppId:     "app1",
		Namespace: "default",
	})
	require.NoError(t, err)

	got := make([]*anypb.Any, num)
	g, err := stream.Recv()
	require.NoError(t, err)
	now = time.Now().UTC()
	fmt.Printf(">>got first    %s\n", now.Format(time.RFC3339))
	diffTime := time.Now().UTC().Sub(inTenT)
	got[0] = g.Data
	for i := 1; i < num; i++ {
		g, err := stream.Recv()
		require.NoError(t, err)
		got[i] = g.Data
		if (i+1)%10000 == 0 {
			fmt.Printf("<<%d\n", i+1)
		}
	}
	triggerTaken := time.Since(now)
	fmt.Printf("write took   %s %05.4fqps (%d/%s)\n", writeTaken, float64(num)/writeTaken.Seconds(), num, writeTaken)
	fmt.Printf("trigger took %s %05.4fqps (%d/%s)\n", triggerTaken, float64(num)/triggerTaken.Seconds(), num, triggerTaken)
	fmt.Printf("first trigger diff %s\n", diffTime)

	gotI := make([]int, num)
	for i := 0; i < num; i++ {
		gotI[i], err = strconv.Atoi(string(got[i].Value))
		require.NoError(t, err)
	}
	sort.Ints(gotI)
	for i := 0; i < num; i++ {
		require.Equal(t, i, gotI[i])
	}
}
