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

package reminders

import (
	"context"
	"errors"

	"github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/internal/scheduler"
	"github.com/dapr/dapr/pkg/actors/table"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
)

// TODO: @joshvanl: move errors package
var (
	ErrReminderOpActorNotHosted = errors.New("operations on actor reminders are only possible on hosted actor types")
	ErrReminderStorageNotSet    = errors.New("reminder scheduler is not configured")
)

type Interface interface {
	// Get retrieves an actor reminder.
	Get(ctx context.Context, actorType, actorID, name string) (*internalsv1pb.Reminder, error)

	// Create creates an actor reminder.
	Create(ctx context.Context, reminder *internalsv1pb.Reminder, isOneShot bool) error

	// Delete deletes an actor reminder.
	Delete(ctx context.Context, actorType, actorID, name string) error
}

type Options struct {
	Scheduler scheduler.Interface
	Table     table.Interface
}

func Key(reminder *internalsv1pb.Reminder) string {
	return reminder.GetActorType() + api.DaprSeparator + reminder.GetActorId() + api.DaprSeparator + reminder.GetName()
}

type reminders struct {
	scheduler scheduler.Interface
	table     table.Interface
}

func New(opts Options) Interface {
	return &reminders{
		scheduler: opts.Scheduler,
		table:     opts.Table,
	}
}
func (r *reminders) Get(ctx context.Context, actorType, actorID, name string) (*internalsv1pb.Reminder, error) {
	if r.scheduler == nil {
		return nil, ErrReminderStorageNotSet
	}

	if !r.table.IsActorTypeHosted(actorType) {
		return nil, ErrReminderOpActorNotHosted
	}

	return r.scheduler.Get(ctx, actorType, actorID, name)
}

func (r *reminders) Create(ctx context.Context, reminder *internalsv1pb.Reminder, isOneShot bool) error {
	if r.scheduler == nil {
		return ErrReminderStorageNotSet
	}

	if !r.table.IsActorTypeHosted(reminder.GetActorType()) {
		return ErrReminderOpActorNotHosted
	}

	return r.scheduler.Create(ctx, reminder, isOneShot)
}

func (r *reminders) Delete(ctx context.Context, actorType, actorID, name string) error {
	if r.scheduler == nil {
		return ErrReminderStorageNotSet
	}

	if !r.table.IsActorTypeHosted(actorType) {
		return ErrReminderOpActorNotHosted
	}

	return r.scheduler.Delete(ctx, actorType, actorID, name)
}
