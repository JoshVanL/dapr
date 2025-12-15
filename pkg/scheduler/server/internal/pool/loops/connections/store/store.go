/*l
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

package store

import (
	"context"
	"fmt"

	"github.com/dapr/dapr/pkg/scheduler/monitoring"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops"
	"github.com/dapr/kit/events/loop"
)

type Options struct {
	Loop            loop.Interface[loops.Event]
	AppID           *string
	ActorTypes      []string
	DurableActorIDs bool
}

// TODO: @joshvanl: Move store routing into stream.
type Store struct {
	appIDs     *instance
	actorTypes *instance

	durableActorIDs []loop.Interface[loops.Event]
}

func New() *Store {
	return &Store{
		appIDs:     newInstance(),
		actorTypes: newInstance(),
	}
}

func (s *Store) Add(opts Options) context.CancelFunc {
	// We don't know how many allocations we will have!
	//nolint:prealloc
	var fns []context.CancelFunc

	if opts.AppID != nil {
		fns = append(fns, s.appIDs.add(*opts.AppID, opts.Loop))
	}

	for _, actorType := range opts.ActorTypes {
		fns = append(fns, s.actorTypes.add(actorType, opts.Loop))
	}

	if opts.DurableActorIDs {
		s.durableActorIDs = append(s.durableActorIDs, opts.Loop)
	}

	fmt.Printf(">>>%p STORE ADDED LOOP. DURABLE LOOPS COUNT: %d\n", s, len(s.durableActorIDs))

	monitoring.RecordSidecarsConnectedCount(1)
	return func() {
		fmt.Printf(">>>%p STORE REMOVING LOOP. DURABLE LOOPS COUNT BEFORE: %d\n", s, len(s.durableActorIDs))
		if opts.DurableActorIDs {
			for i := range s.durableActorIDs {
				if s.durableActorIDs[i] == opts.Loop {
					s.durableActorIDs = append(s.durableActorIDs[:i], s.durableActorIDs[i+1:]...)
					break
				}
			}
		}

		for _, fn := range fns {
			fn()
		}

		opts.Loop.Close(new(loops.StreamShutdown))
		monitoring.RecordSidecarsConnectedCount(-1)
		fmt.Printf(">>>STORE REMOVED LOOP. DURABLE LOOPS COUNT AFTER: %d\n", len(s.durableActorIDs))
	}
}

func (s *Store) AppID(id string) (loop.Interface[loops.Event], bool) {
	return s.appIDs.get(id)
}

func (s *Store) ActorType(actorType string) (loop.Interface[loops.Event], bool) {
	return s.actorTypes.get(actorType)
}

func (s *Store) DurableActorIDs() []loop.Interface[loops.Event] {
	fmt.Printf(">>>%p STORE RETURNING DURABLE LOOPS: %d\n", s, len(s.durableActorIDs))
	return s.durableActorIDs
}
