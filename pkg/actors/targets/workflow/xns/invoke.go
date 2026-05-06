/*
Copyright 2026 The Dapr Authors
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

package xns

import (
	"context"
	"errors"
	"fmt"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	actorapi "github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
)

// InvokeMethod handles the Schedule entry point. The request body is a
// marshalled ForwardOpRequest; the metadata carries the direction
// (outbound|inbound). The actor executes the op inline on the happy path
// and uses an actor reminder as the durability backstop. The reminder
// payload carries the input op, so the actor itself is stateless.
func (x *xns) InvokeMethod(ctx context.Context, req *internalsv1pb.InternalInvokeRequest) (*internalsv1pb.InternalInvokeResponse, error) {
	unlock, err := x.lock.ContextLock(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()

	method := req.GetMessage().GetMethod()
	if method != ScheduleMethod {
		return nil, fmt.Errorf("xns actor: unknown method %q", method)
	}

	dirVals := req.GetMetadata()[metadataDirection]
	if dirVals == nil || len(dirVals.GetValues()) == 0 {
		return nil, errors.New("xns Schedule: missing direction metadata")
	}
	direction := Direction(dirVals.GetValues()[0])
	rname, err := direction.reminderName()
	if err != nil {
		return nil, fmt.Errorf("xns Schedule: %w (got %q)", err, direction)
	}

	body := req.GetMessage().GetData().GetValue()
	op := new(internalsv1pb.ForwardOpRequest)
	if err := proto.Unmarshal(body, op); err != nil {
		return nil, fmt.Errorf("xns Schedule: unmarshal op: %w", err)
	}

	// Register the durable backstop reminder before attempting work
	// inline. The reminder data carries the input so InvokeReminder is
	// self-contained on a crash-recovery path.
	if err := x.scheduleReminder(ctx, rname, body); err != nil {
		return nil, fmt.Errorf("xns Schedule: register reminder: %w", err)
	}

	// Synchronous attempt. On success the reminder is cleared so a stale
	// fire does not re-execute the op. On failure the reminder retries
	// per its failure policy.
	resp, execErr := x.execute(ctx, direction, op)
	if execErr != nil {
		log.Debugf("xns actor '%s': synchronous attempt failed; reminder will retry: %v", x.actorID, execErr)
		return nil, execErr
	}

	x.deleteReminder(ctx, rname)
	return wrapResponse(resp)
}

// InvokeReminder is the durable backstop. If the synchronous attempt in
// InvokeMethod did not delete this reminder (because it crashed,
// returned an error, or never ran), the reminder fires here, replays the
// op from its own payload, and on success deletes itself.
func (x *xns) InvokeReminder(ctx context.Context, reminder *actorapi.Reminder) error {
	unlock, err := x.lock.ContextLock(ctx)
	if err != nil {
		return err
	}
	defer unlock()

	var direction Direction
	switch reminder.Name {
	case reminderOutbound:
		direction = DirectionOutbound
	case reminderInbound:
		direction = DirectionInbound
	default:
		return fmt.Errorf("xns InvokeReminder: unknown reminder %q", reminder.Name)
	}

	body, err := unmarshalReminderData(reminder.Data)
	if err != nil {
		return fmt.Errorf("xns InvokeReminder: %w", err)
	}
	op := new(internalsv1pb.ForwardOpRequest)
	if err := proto.Unmarshal(body, op); err != nil {
		return fmt.Errorf("xns InvokeReminder: unmarshal op: %w", err)
	}

	if _, err := x.execute(ctx, direction, op); err != nil {
		// Returning the error keeps the reminder pending; the failure
		// policy retries on its schedule.
		return err
	}
	x.deleteReminder(ctx, reminder.Name)
	return nil
}

// execute dispatches to the direction-specific action. Outbound and
// inbound implementations live in their own files (outbound.go,
// inbound.go) to keep each side's logic in one place.
func (x *xns) execute(ctx context.Context, dir Direction, op *internalsv1pb.ForwardOpRequest) (*internalsv1pb.ForwardOpResponse, error) {
	switch dir {
	case DirectionOutbound:
		return x.outbound(ctx, op)
	case DirectionInbound:
		return x.inbound(ctx, op)
	default:
		return nil, fmt.Errorf("xns: invalid direction %q", dir)
	}
}

// scheduleReminder registers the durable backstop. Fires immediately as
// a one-shot; on transient failure the failure policy retries every 30s
// indefinitely. The data carries the marshalled ForwardOpRequest so the
// reminder is self-contained.
func (x *xns) scheduleReminder(ctx context.Context, name string, opBytes []byte) error {
	data, err := anypb.New(wrapperspb.Bytes(opBytes))
	if err != nil {
		return err
	}
	return common.CreateReminderWithRetry(ctx, x.reminders, &actorapi.CreateReminderRequest{
		ActorType: x.actorType,
		ActorID:   x.actorID,
		Name:      name,
		DueTime:   "0s",
		FailurePolicy: &commonv1pb.JobFailurePolicy{
			Policy: &commonv1pb.JobFailurePolicy_Constant{
				Constant: &commonv1pb.JobFailurePolicyConstant{
					Interval:   durationpb.New(30 * time.Second),
					MaxRetries: nil,
				},
			},
		},
		Data: data,
	})
}

func (x *xns) deleteReminder(ctx context.Context, name string) {
	if err := x.reminders.Delete(ctx, &actorapi.DeleteReminderRequest{
		ActorType: x.actorType,
		ActorID:   x.actorID,
		Name:      name,
	}); err != nil {
		log.Debugf("xns actor '%s': delete reminder %q: %v", x.actorID, name, err)
	}
}

// unmarshalReminderData extracts the original ForwardOpRequest bytes
// from a reminder's anypb payload. Reminders wrap user data in
// google.protobuf.BytesValue (see actor reminder serialisation).
func unmarshalReminderData(data *anypb.Any) ([]byte, error) {
	if data == nil {
		return nil, errors.New("reminder has no data")
	}
	bv := new(wrapperspb.BytesValue)
	if err := data.UnmarshalTo(bv); err != nil {
		return nil, fmt.Errorf("decode reminder data: %w", err)
	}
	return bv.GetValue(), nil
}

func wrapResponse(resp *internalsv1pb.ForwardOpResponse) (*internalsv1pb.InternalInvokeResponse, error) {
	if resp == nil {
		resp = &internalsv1pb.ForwardOpResponse{}
	}
	body, err := proto.Marshal(resp)
	if err != nil {
		return nil, err
	}
	return &internalsv1pb.InternalInvokeResponse{
		Status: &internalsv1pb.Status{Code: 200},
		Message: &commonv1pb.InvokeResponse{Data: &anypb.Any{Value: body}},
	}, nil
}
