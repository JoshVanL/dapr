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

package store

import (
	"testing"

	"github.com/stretchr/testify/assert"

	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
)

func TestSet_NewHostReturnsAllEntities(t *testing.T) {
	s := New(Options{ReplicationFactor: 100})

	changed, diff := s.Set(1, &v1pb.Host{
		Name:      "host-1",
		Id:        "app-1",
		Namespace: "default",
		Entities:  []string{"actorB", "actorA"},
	})

	assert.True(t, changed)
	assert.ElementsMatch(t, []string{"actorA", "actorB"}, diff,
		"new host must report its full entity set as changed")
}

func TestSet_UnchangedHostReturnsNoDiff(t *testing.T) {
	s := New(Options{ReplicationFactor: 100})

	host := &v1pb.Host{
		Name:      "host-1",
		Id:        "app-1",
		Namespace: "default",
		Entities:  []string{"actorA"},
	}
	s.Set(1, host)

	changed, diff := s.Set(1, &v1pb.Host{
		Name:      "host-1",
		Id:        "app-1",
		Namespace: "default",
		Entities:  []string{"actorA"},
	})

	assert.False(t, changed)
	assert.Empty(t, diff)
}

func TestSet_AddedAndRemovedEntities(t *testing.T) {
	s := New(Options{ReplicationFactor: 100})

	s.Set(1, &v1pb.Host{
		Name:      "host-1",
		Id:        "app-1",
		Namespace: "default",
		Entities:  []string{"actorA", "actorB"},
	})

	// Add actorC, remove actorB. Diff is symmetric: {actorB, actorC}.
	changed, diff := s.Set(1, &v1pb.Host{
		Name:      "host-1",
		Id:        "app-1",
		Namespace: "default",
		Entities:  []string{"actorA", "actorC"},
	})

	assert.True(t, changed)
	assert.ElementsMatch(t, []string{"actorB", "actorC"}, diff)
}

func TestSet_HostClearedReturnsPriorEntities(t *testing.T) {
	s := New(Options{ReplicationFactor: 100})

	s.Set(1, &v1pb.Host{
		Name:      "host-1",
		Id:        "app-1",
		Namespace: "default",
		Entities:  []string{"actorA", "actorB"},
	})

	changed, diff := s.Set(1, &v1pb.Host{
		Name:      "host-1",
		Id:        "app-1",
		Namespace: "default",
		Entities:  nil,
	})

	assert.True(t, changed)
	assert.ElementsMatch(t, []string{"actorA", "actorB"}, diff,
		"clearing entities must report the prior entity set as changed")
	assert.False(t, s.Has(1), "cleared host must be removed from store")
}

func TestEntitiesOf(t *testing.T) {
	s := New(Options{ReplicationFactor: 100})

	assert.Nil(t, s.EntitiesOf(99), "missing stream returns nil")

	s.Set(1, &v1pb.Host{
		Name:      "host-1",
		Id:        "app-1",
		Namespace: "default",
		Entities:  []string{"actorB", "actorA"},
	})
	got := s.EntitiesOf(1)
	assert.ElementsMatch(t, []string{"actorA", "actorB"}, got)

	// Mutating the returned slice must not affect the store.
	got[0] = "mutated"
	again := s.EntitiesOf(1)
	assert.ElementsMatch(t, []string{"actorA", "actorB"}, again,
		"EntitiesOf must return a clone, not a shared slice")
}
