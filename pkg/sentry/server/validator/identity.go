/*
Copyright 2023 The Dapr Authors
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

package validator

import (
	"github.com/spiffe/go-spiffe/v2/spiffeid"
)

type Identity struct {
	trustDomain spiffeid.TrustDomain
	podName     *string
}

func NewIdentity(trustDomain spiffeid.TrustDomain, podName *string) *Identity {
	return &Identity{
		trustDomain: trustDomain,
		podName:     podName,
	}
}

func (i *Identity) TrustDomain() spiffeid.TrustDomain {
	return i.trustDomain
}

func (i *Identity) PodName() (string, bool) {
	if i.podName == nil {
		return "", false
	}
	return *i.podName, true
}
