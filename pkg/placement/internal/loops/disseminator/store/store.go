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
	"fmt"
	"strconv"

	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
)

type Options struct {
	ReplicationFactor int64
}

type Store struct {
	replicationFactor int64

	// hosts are indexed on streamIDx.
	hosts map[uint64]*v1pb.Host
}

func New(opts Options) *Store {
	return &Store{
		replicationFactor: opts.ReplicationFactor,
		hosts:             make(map[uint64]*v1pb.Host),
	}
}

func (s *Store) PlacementTables(version uint64) *v1pb.PlacementTables {
	t := &v1pb.PlacementTables{
		ReplicationFactor: s.replicationFactor,
		Entries:           make(map[string]*v1pb.PlacementTable),
		Version:           strconv.FormatUint(version, 10),
	}

	for streamID, host := range s.hosts {
		fmt.Printf(">>RETURNING TABLE WITH HOST: %d:%s\n", streamID, host.Name)
		for _, entity := range host.Entities {
			if t.Entries[entity] == nil {
				t.Entries[entity] = &v1pb.PlacementTable{
					LoadMap: make(map[string]*v1pb.Host),
				}
			}

			t.Entries[entity].LoadMap[host.Name] = host
		}
	}

	fmt.Printf(">>--------------\n")

	return t
}

func (s *Store) Set(streamIDx uint64, host *v1pb.Host) {
	s.hosts[streamIDx] = host
}

func (s *Store) Delete(streamIDx uint64) {
	delete(s.hosts, streamIDx)
}

func (s *Store) DeleteAll() {
	clear(s.hosts)
}
