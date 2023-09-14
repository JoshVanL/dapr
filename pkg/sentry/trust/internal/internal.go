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

package internal

import (
	"context"
	"sync"

	"github.com/dapr/dapr/pkg/concurrency"
)

// TrustAnchors is the current trust anchors is different formats.
type TrustAnchors struct {
	lock sync.RWMutex

	// pem is the trust anchors in PEM format.
	pem []byte
}

type TrustAnchorsChannel <-chan *TrustAnchors

// NewTrustAnchors returns a new trust anchors.
func NewTrustAnchors(pem ...byte) *TrustAnchors {
	return &TrustAnchors{
		pem: pem,
	}
}

// PEM returns the trust anchors in PEM format.
func (t *TrustAnchors) PEM() []byte {
	t.lock.RLock()
	defer t.lock.RUnlock()
	return t.pem
}

// PEMString returns the trust anchors in PEM format as a string.
func (t *TrustAnchors) PEMString() string {
	t.lock.RLock()
	defer t.lock.RUnlock()
	return string(t.pem)
}

// DeepCopy returns a deep copy of the trust anchors.
func (t *TrustAnchors) DeepCopy() *TrustAnchors {
	t.lock.RLock()
	defer t.lock.RUnlock()
	nt := &TrustAnchors{
		pem: make([]byte, len(t.pem)),
	}
	copy(nt.pem, t.pem)
	return nt
}

// AppendPEM appends the given PEM encoded trust anchors to the current trust
func (t *TrustAnchors) AppendPEM(pem []byte) {
	t.lock.Lock()
	defer t.lock.Unlock()
	t.pem = append(t.pem, pem...)
}

// Source is the interface for fetching and reporting the current X.509 PEM
// encoded trust anchors.
type Source interface {
	// Run starts the trust source.
	Run(context.Context) error

	// Latest returns the current trust anchors.
	Latest(context.Context) (*TrustAnchors, error)

	// Subscribe returns a channel that will be notified when the trust anchors
	// are updated with the latest trust anchors.
	Subscribe() TrustAnchorsChannel
}

// Distributor is the interface for a trust distributor which is responsible
// for distributing the trust anchors to Dapr instances.
type Distributor concurrency.Runner
