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

package disk

import (
	"context"
	"sort"

	subv1api "github.com/dapr/dapr/pkg/apis/subscriptions/v1alpha1"
	subv2api "github.com/dapr/dapr/pkg/apis/subscriptions/v2alpha1"
	"github.com/dapr/dapr/pkg/internal/loader"
)

type subscriptions struct {
	v1 *disk[subv1api.Subscription]
	v2 *disk[subv2api.Subscription]
}

func NewSubscriptions(paths ...string) loader.Loader[subv2api.Subscription] {
	return &subscriptions{
		v1: New[subv1api.Subscription](paths...),
		v2: New[subv2api.Subscription](paths...),
	}
}

func (s *subscriptions) Load(context.Context) ([]subv2api.Subscription, error) {
	v1, err := s.v1.loadWithOrder()
	if err != nil {
		return nil, err
	}

	v2, err := s.v2.loadWithOrder()
	if err != nil {
		return nil, err
	}

	v2.order = append(v2.order, v1.order...)

	for _, s := range v1.ts {
		var subv2 subv2api.Subscription
		if err := subv2.ConvertFrom(s.DeepCopy()); err != nil {
			return nil, err
		}

		v2.ts = append(v2.ts, subv2)
	}

	// Preserve manifest load order between v1 and v2.
	sort.Sort(v2)

	return v2.ts, nil
}
