/*
Copyright 2023 The Dapr Authors
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

package server

import (
	"context"
	"fmt"
	"time"

	etcdcron "github.com/diagridio/go-etcd-cron"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/anypb"

	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/pkg/proto/runtime/v1"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	timeutils "github.com/dapr/kit/time"
)

func (s *Server) ConnectHost(context.Context, *schedulerv1pb.ConnectHostRequest) (*schedulerv1pb.ConnectHostResponse, error) {
	return nil, fmt.Errorf("not implemented")
}

func (s *Server) ScheduleJob(ctx context.Context, req *schedulerv1pb.ScheduleJobRequest) (*schedulerv1pb.ScheduleJobResponse, error) {
	// TODO(artursouza): Add authorization check between caller and request.
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.readyCh:
	}

	startTime, err := parseStartTime(req.GetJob().GetDueTime())
	if err != nil {
		return nil, fmt.Errorf("error parsing due time: %w", err)
	}
	ttl, err := parseTTL(req.GetJob().GetTtl())
	if err != nil {
		return nil, fmt.Errorf("error parsing TTL: %w", err)
	}

	job := etcdcron.Job{
		Name:      req.GetJob().GetName(),
		Rhythm:    req.GetJob().GetSchedule(),
		Repeats:   req.GetJob().GetRepeats(),
		StartTime: startTime,
		TTL:       ttl,
		Payload:   req.GetJob().GetData(),
		Metadata:  req.GetMetadata(),
	}

	err = s.cron.AddJob(ctx, job)
	if err != nil {
		log.Errorf("error scheduling job %s: %s", req.GetJob().GetName(), err)
		return nil, err
	}

	return &schedulerv1pb.ScheduleJobResponse{}, nil
}

func (s *Server) triggerJob(ctx context.Context, metadata map[string]string, payload *anypb.Any) (etcdcron.TriggerResult, error) {
	log.Debug("Triggering job")
	actorType := metadata["actorType"]
	actorID := metadata["actorId"]
	reminderName := metadata["reminder"]
	if actorType != "" && actorID != "" && reminderName != "" {
		if s.actorRuntime == nil {
			return etcdcron.Failure, fmt.Errorf("actor runtime is not configured")
		}

		invokeMethod := "remind/" + reminderName
		contentType := metadata["content-type"]
		invokeReq := internalv1pb.NewInternalInvokeRequest(invokeMethod).
			WithActor(actorType, actorID).
			WithData(payload.GetValue()).
			WithContentType(contentType)

		res, err := s.actorRuntime.Call(ctx, invokeReq)
		if err != nil {
			return etcdcron.Failure, err
		}

		if res.GetStatus().GetCode() != int32(codes.OK) {
			return etcdcron.Failure, nil
		}

		return etcdcron.OK, err
	}

	// TODO(artursouza): echo the job's name instead once we change the library's callback method.
	log.Warn("Cannot trigger job: %v", metadata)
	return etcdcron.Failure, nil
}

func (s *Server) DeleteJob(ctx context.Context, req *schedulerv1pb.DeleteJobRequest) (*schedulerv1pb.DeleteJobResponse, error) {
	// TODO(artursouza): Add authorization check between caller and request.
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.readyCh:
	}

	jobName := req.GetJobName()
	err := s.cron.DeleteJob(ctx, jobName)
	if err != nil {
		log.Errorf("error deleting job %s: %s", jobName, err)
		return nil, err
	}

	return &schedulerv1pb.DeleteJobResponse{}, nil
}

func (s *Server) GetJob(ctx context.Context, req *schedulerv1pb.GetJobRequest) (*schedulerv1pb.GetJobResponse, error) {
	// TODO(artursouza): Add authorization check between caller and request.
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.readyCh:
	}

	jobName := req.GetJobName()
	// TODO(artursouza): Use "FetchJob()" instead to read from db instead of running jobs only - once method exists.
	job := s.cron.GetJob(jobName)
	if job == nil {
		return nil, fmt.Errorf("job not found: %s", jobName)
	}

	ttl := ""
	if job.TTL > 0 {
		ttl = job.TTL.String()
	}

	return &schedulerv1pb.GetJobResponse{
		Job: &runtime.Job{
			Name:     jobName,
			Schedule: job.Rhythm,
			Repeats:  job.Repeats,
			Ttl:      ttl,
			DueTime:  job.StartTime.Format(time.RFC3339),
			Data:     job.Payload,
		},
	}, nil
}

// parseStartTime is a wrapper around timeutils.ParseTime that truncates the time to seconds.
func parseStartTime(dueTime string) (time.Time, error) {
	if dueTime == "" {
		return time.Time{}, nil
	}

	now := time.Now()
	t, err := timeutils.ParseTime(dueTime, &now)
	if err != nil {
		return t, err
	}
	t = t.Truncate(time.Second)
	return t, nil
}

func parseTTL(ttl string) (time.Duration, error) {
	if ttl == "" {
		return time.Duration(0), nil
	}

	years, months, days, period, _, err := timeutils.ParseDuration(ttl)
	if err != nil {
		return time.Duration(0), fmt.Errorf("parse error: %w", err)
	}
	if (years == 0) && (months == 0) && (days == 0) {
		// Optimization to avoid the complex calculation below
		return period, nil
	}

	return time.Until(time.Now().AddDate(years, months, days).Add(period)), nil
}
