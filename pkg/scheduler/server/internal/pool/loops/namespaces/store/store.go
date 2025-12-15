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
	"fmt"

	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops"
)

//type Options struct {
//	Loop            loop.Interface[loops.Event]
//	AppID           *string
//	ActorTypes      []string
//	DurableActorIDs bool
//}

type Store struct {
	// store of durable actor ids.
	// Idexed by namespace -> actor type -> actor id -> job.
	durableActorIDs map[string]map[string]map[string]*loops.BroadcastJob
}

func New() *Store {
	return &Store{
		durableActorIDs: make(map[string]map[string]map[string]*loops.BroadcastJob),
	}
}

func (s *Store) AddDurableActorID(job *loops.BroadcastJob) {
	fmt.Printf(">>NAMESPACE STORE ADDING DURABLE ACTOR ID: %s/%s/%s\n", job.Metadata.Namespace, job.Metadata.GetTarget().GetBroadcast().GetDurableActorId().GetType(), job.Name)

	ns, ok := s.durableActorIDs[job.Metadata.Namespace]
	if !ok {
		ns = make(map[string]map[string]*loops.BroadcastJob)
		s.durableActorIDs[job.Metadata.Namespace] = ns
	}

	actorType := job.Metadata.GetTarget().GetBroadcast().GetDurableActorId().GetType()
	atype, ok := ns[actorType]
	if !ok {
		atype = make(map[string]*loops.BroadcastJob)
		ns[actorType] = atype
	}

	atype[job.Name] = job
}

func (s *Store) DeleteDurableActorID(job *loops.BroadcastJob) {
	fmt.Printf(">>NAMESPACE STORE DELETING DURABLE ACTOR ID: %s/%s/%s\n", job.Metadata.Namespace, job.Metadata.GetTarget().GetBroadcast().GetDurableActorId().GetType(), job.Name)
	ns, ok := s.durableActorIDs[job.Metadata.Namespace]
	if !ok {
		return
	}

	actorType := job.Metadata.GetTarget().GetBroadcast().GetDurableActorId().GetType()
	atype, ok := ns[actorType]
	if !ok {
		return
	}

	delete(atype, job.Name)
	if len(atype) == 0 {
		delete(ns, actorType)
	}
	if len(ns) == 0 {
		delete(s.durableActorIDs, job.Metadata.Namespace)
	}
}

// TODO: @joshvanl: do by actor type as well
func (s *Store) GetDurableActorIDs(namespace string) []*loops.BroadcastJob {
	fmt.Printf(">>NAMESPACE STORE GETTING DURABLE ACTOR IDS FOR NAMESPACE: %s\n", namespace)

	ns, ok := s.durableActorIDs[namespace]
	if !ok {
		fmt.Printf(">>NAMESPACE STORE NO DURABLE ACTOR IDS FOR NAMESPACE: %s\n", namespace)
		return nil
	}

	var jobs []*loops.BroadcastJob
	for _, atype := range ns {
		for _, job := range atype {
			jobs = append(jobs, job)
		}
	}

	fmt.Printf(">>NAMESPACE STORE FOUND %d DURABLE ACTOR IDS FOR NAMESPACE: %s\n", len(jobs), namespace)
	return jobs
}

func (s *Store) ClearDurableActorIDs() {
	clear(s.durableActorIDs)
}
