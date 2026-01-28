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

package lock

import (
	"context"
	"fmt"
	"time"

	"github.com/dapr/kit/events/loop"
)

type Event any

type Claim struct {
	Context context.Context
	Cancel  context.CancelCauseFunc
}

type Aquire struct {
	Context context.Context
	RespCh  chan *Claim
}

type releaseClaim struct {
	idx uint64
}

type CloseLock struct {
	Error error
}

type lock struct {
	idx      uint64
	acquires map[uint64]*Claim

	loop loop.Interface[Event]
}

func New() loop.Interface[Event] {
	l := &lock{
		acquires: make(map[uint64]*Claim),
	}

	// TODO: @joshvanl: cache loops.
	l.loop = loop.New[Event](1024).NewLoop(l)
	return l.loop
}

func (l *lock) Handle(_ context.Context, event Event) error {
	switch e := event.(type) {
	case *Aquire:
		l.handleAquire(e)
	case *releaseClaim:
		l.handleRelease(e)
	case *CloseLock:
		l.handleClose(e)
	default:
		panic(fmt.Sprintf("unknown lock event type: %T", e))
	}

	return nil
}

func (l *lock) handleClose(closeLock *CloseLock) {
	defer func() {
		fmt.Printf(">>HANDLE CLOSE DONE\n")
	}()

	// Grace period to allow claims to be released.
	timer := time.NewTimer(time.Second * 2)
	defer timer.Stop()

	fmt.Printf(">>IN HANDLE CLOSE: %d\n", len(l.acquires))
	for i, claim := range l.acquires {
		select {
		case <-claim.Context.Done():
			fmt.Printf(">>CLAIM ALREADY DONE: %d\n", i)
		case <-timer.C:
			// TODO: @joshvanl: add log
			// Force cancel all remaining claims after timeout.
			for i, claim := range l.acquires {
				fmt.Printf(">>CANCELLING CLAIM: %d\n", i)
				claim.Cancel(closeLock.Error)
				fmt.Printf(">>CANCELLED CLAIM: %d\n", i)
			}
			return
		}
	}

	fmt.Printf(">>OUT HANDLE CLOSE\n")
}

func (l *lock) handleRelease(release *releaseClaim) {
	delete(l.acquires, release.idx)
}

func (l *lock) handleAquire(event *Aquire) {
	idx := l.idx
	l.idx++

	var done bool

	ctx, cancel := context.WithCancelCause(event.Context)
	claim := &Claim{
		Context: ctx,
		Cancel: func(err error) {
			if done {
				return
			}
			done = true
			cancel(err)
			fmt.Printf(">>HERE1: %d\n", idx)
			l.loop.Enqueue(&releaseClaim{idx: idx})
			fmt.Printf(">>HERE2: %d\n", idx)
		},
	}

	l.acquires[idx] = claim
	event.RespCh <- claim
}
