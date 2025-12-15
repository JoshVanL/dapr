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

package serialize

import (
	"errors"
	"fmt"
	"reflect"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
)

func ValidateSchedule(req *schedulerv1pb.ScheduleJobRequest) error {
	b := req.GetMetadata().GetTarget().GetBroadcast()
	if b == nil {
		return nil
	}

	job := req.GetJob()
	var errs []error
	for _, f := range []struct {
		value any
		name  string
	}{
		{job.FailurePolicy, "failure_policy"},
		{job.Repeats, "repeats"},
		{job.Schedule, "schedule"},
		{job.Ttl, "ttl"},
	} {
		if err := assertNil(f.value, f.name); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	for _, f := range []struct {
		value any
		name  string
	}{
		{job.DueTime, "due_time"},
	} {
		if err := assertNotNil(f.value, f.name); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
}

func assertNotNil(i any, field string) error {
	if reflect.ValueOf(i).IsNil() {
		return errors.New(field + " must be set for non-broadcast target")
	}

	return nil
}

func assertNil(i any, field string) error {
	if !reflect.ValueOf(i).IsNil() {
		return fmt.Errorf("%s must be nil for broadcast target", field)
	}

	return nil
}
