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

// Package fake provides an in-process xns.Forwarder for tests. The
// production Forwarder dials the peer daprd via Dapr service-invocation
// (SPIFFE-mTLS, resolver-driven). The fake skips the network and instead
// invokes a Receiver registered for the target (namespace, appID) pair,
// which lets integration tests exercise the bridge end-to-end without
// service-invocation wiring.
package fake

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/dapr/dapr/pkg/actors/targets/workflow/xns"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
)

// Hub maintains a registry of (namespace, appID) → Receiver and exposes
// a Forwarder per source app. Tests register each app's Receiver on
// startup; the Forwarder looks up the target Receiver from the
// ForwardOpRequest's TargetAppNamespace + TargetAppId fields.
type Hub struct {
	mu        sync.RWMutex
	receivers map[string]xns.Receiver // key = "<ns>/<appID>"
}

func NewHub() *Hub {
	return &Hub{receivers: make(map[string]xns.Receiver)}
}

func (h *Hub) Register(namespace, appID string, r xns.Receiver) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.receivers[key(namespace, appID)] = r
}

// Forwarder returns an xns.Forwarder backed by this Hub. Every outbound
// app shares the same Forwarder instance because the routing decision
// is made entirely from the request fields.
func (h *Hub) Forwarder() xns.Forwarder {
	return &forwarder{hub: h}
}

type forwarder struct {
	hub *Hub
}

func (f *forwarder) Forward(ctx context.Context, req *internalsv1pb.ForwardOpRequest) (*internalsv1pb.ForwardOpResponse, error) {
	if req == nil {
		return nil, errors.New("fake forwarder: nil request")
	}
	targetNS := req.GetTargetAppNamespace()
	if targetNS == "" {
		return nil, errors.New("fake forwarder: ForwardOpRequest is missing target_app_namespace")
	}
	f.hub.mu.RLock()
	rx, found := f.hub.receivers[key(targetNS, req.GetTargetAppId())]
	f.hub.mu.RUnlock()
	if !found {
		return nil, fmt.Errorf("fake forwarder: no receiver registered for %s/%s", targetNS, req.GetTargetAppId())
	}
	return rx.Execute(ctx, req)
}

func key(ns, appID string) string { return ns + "/" + appID }
