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
	"errors"
	"fmt"
	"time"

	kittime "github.com/dapr/kit/time"
)

type period struct {
	value string // Raw value as received from the user

	years   int
	months  int
	days    int
	period  time.Duration
	repeats int
}

// newPeriod parses a reminder period from a string and validates it.
func newPeriod(val string) (*period, error) {
	p := newEmptyPeriod()

	var err error
	if val != "" {
		p.value = val
		err = parsePeriod(p)
	}

	return p, err
}

// newEmptyPeriod returns an empty Period, which has unlimited repeats.
func newEmptyPeriod() *period {
	return &period{
		repeats: -1,
	}
}

// newSchedulerPeriod returns a new reminder period from the Scheduler
// service job schedule.
func newSchedulerPeriod(val string, repeats uint32) *period {
	p := newEmptyPeriod()
	p.repeats = int(repeats)
	p.value = val

	return p
}

func (p *period) hasRepeats() bool {
	return p.repeats != 0 &&
		(p.years != 0 || p.months != 0 || p.days != 0 || p.period != 0)
}

func (p *period) getFollowing(t time.Time) time.Time {
	return t.AddDate(p.years, p.months, p.days).Add(p.period)
}

func parsePeriod(p *period) (err error) {
	p.years, p.months, p.days, p.period, p.repeats, err = kittime.ParseDuration(p.value)
	if err != nil {
		return fmt.Errorf("parse error: %w", err)
	}

	// Error on timers with zero repetitions
	if p.repeats == 0 {
		return errors.New("has zero repetitions")
	}

	return nil
}
