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

package item

import (
	"fmt"
	"time"

	"github.com/dapr/dapr/pkg/actors/reminders"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	kittime "github.com/dapr/kit/time"
)

type Item struct {
	reminder       *internalsv1pb.Reminder
	callback       string
	registeredTime time.Time
	expirationTime time.Time

	period *period
}

func New(reminder *internalsv1pb.Reminder, callback string) (*Item, error) {
	period, err := newPeriod(reminder.Period)
	if err != nil {
		return nil, fmt.Errorf("invalid period: %w", err)
	}

	now := time.Now()
	registeredTime := now
	if len(reminder.GetDueTime()) > 0 {
		registeredTime, err = kittime.ParseTime(reminder.GetDueTime(), &now)
		if err != nil {
			return nil, err
		}
	}

	var expirationTime time.Time
	if reminder.ExpirationTime != nil {
		expirationTime = reminder.ExpirationTime.AsTime()

		if now.After(expirationTime) || registeredTime.After(expirationTime) {
			return nil, fmt.Errorf("%s has already expired: dueTime: %s TTL: %s",
				reminders.Key(reminder), registeredTime, reminder.ExpirationTime.AsTime())
		}
	}

	return &Item{
		reminder:       reminder,
		callback:       callback,
		period:         period,
		registeredTime: registeredTime,
		expirationTime: expirationTime,
	}, nil
}

func (i *Item) Reminder() *internalsv1pb.Reminder {
	return i.reminder
}

func (i *Item) Callback() string {
	return i.callback
}

// Key returns the key for this unique reminder.
func (i *Item) Key() string {
	return reminders.Key(i.reminder)
}

// NextTick returns the time the reminder should tick again next.
// If the reminder has a TTL and the next tick is beyond the TTL, the second returned value will be false.
func (i *Item) NextTick() (time.Time, bool) {
	active := i.expirationTime.IsZero() || i.registeredTime.Before(i.expirationTime)
	return i.registeredTime, active
}

// ScheduledTime returns the time the reminder is scheduled to be executed at.
// This is implemented to comply with the queueable interface.
func (i *Item) ScheduledTime() time.Time {
	return i.registeredTime
}

// TickExecuted should be called after a reminder has been executed.
// "done" will be true if the reminder is done, i.e. no more executions should happen.
// If the reminder is not done, call "NextTick" to get the time it should tick next.
// Note: this method is not concurrency-safe.
func (i *Item) TickExecuted() (done bool) {
	if i.period.repeats > 2 {
		i.period.repeats--
	}

	if !i.period.hasRepeats() {
		return true
	}

	i.registeredTime = i.period.getFollowing(i.registeredTime)

	return false
}
