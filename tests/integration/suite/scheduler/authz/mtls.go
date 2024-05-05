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

package authz

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(mtls))
}

// mtls tests scheduler with tls enabled.
type mtls struct {
	sentry    *sentry.Sentry
	scheduler *scheduler.Scheduler
}

func (m *mtls) Setup(t *testing.T) []framework.Option {
	m.sentry = sentry.New(t)
	m.scheduler = scheduler.New(t, scheduler.WithSentry(m.sentry))

	return []framework.Option{
		framework.WithProcesses(m.sentry, m.scheduler),
	}
}

// TODO: @joshvanl: expand
func (m *mtls) Run(t *testing.T, ctx context.Context) {
	m.sentry.WaitUntilRunning(t, ctx)
	m.scheduler.WaitUntilRunning(t, ctx)

	client := m.scheduler.ClientMTLS(t, ctx, "foo")

	req := &schedulerv1pb.ScheduleJobRequest{
		Name: "testJob",
		Job: &schedulerv1pb.Job{
			Schedule: ptr.Of("@daily"),
		},
		Metadata: &schedulerv1pb.ScheduleJobMetadata{
			AppId:     "foo",
			Namespace: "default",
			Type: &schedulerv1pb.ScheduleJobMetadataType{
				Type: &schedulerv1pb.ScheduleJobMetadataType_Job{
					Job: new(schedulerv1pb.ScheduleTypeJob),
				},
			},
		},
	}

	_, err := client.ScheduleJob(ctx, req)
	require.NoError(t, err)
}
