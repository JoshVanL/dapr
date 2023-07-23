/*
Copyright 2022 The Dapr Authors
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

package universal

import (
	"context"
	"fmt"

	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/dapr/dapr/pkg/buildinfo"
	"github.com/dapr/dapr/pkg/config/protocol"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	runtimePubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
)

const daprRuntimeVersionKey = "daprRuntimeVersion"

func (u *Universal) GetMetadata(ctx context.Context, in *emptypb.Empty) (*runtimev1pb.GetMetadataResponse, error) {
	// Extended metadata
	extendedMetadata := make(map[string]string, len(u.extendedMetadata)+1)
	u.extendedMetadataLock.RLock()
	for k, v := range u.extendedMetadata {
		extendedMetadata[k] = v
	}
	u.extendedMetadataLock.RUnlock()

	// This is deprecated, but we still need to support it for backward compatibility.
	extendedMetadata[daprRuntimeVersionKey] = buildinfo.Version()

	// Active actors count
	activeActorsCount := []*runtimev1pb.ActiveActorsCount{}
	if u.actors != nil {
		activeActorsCount = u.actors.GetActiveActorsCount(ctx)
	}

	// App connection information
	appConnectionProperties := &runtimev1pb.AppConnectionProperties{
		ChannelAddress: u.appConnectionConfig.ChannelAddress,
		Port:           int32(u.appConnectionConfig.Port),
		Protocol:       string(u.appConnectionConfig.Protocol),
		MaxConcurrency: int32(u.appConnectionConfig.MaxConcurrency),
	}

	if u.appConnectionConfig.HealthCheck != nil {
		appConnectionProperties.Health = &runtimev1pb.AppConnectionHealthProperties{
			HealthProbeInterval: u.appConnectionConfig.HealthCheck.ProbeInterval.String(),
			HealthProbeTimeout:  u.appConnectionConfig.HealthCheck.ProbeTimeout.String(),
			HealthThreshold:     u.appConnectionConfig.HealthCheck.Threshold,
		}

		// Health check path is not applicable for gRPC.
		if protocol.Protocol(appConnectionProperties.Protocol).IsHTTP() {
			appConnectionProperties.Health.HealthCheckPath = u.appConnectionConfig.HealthCheckHTTPPath
		}
	}

	// Components
	components := u.compStore.ListComponents()
	registeredComponents := make([]*runtimev1pb.RegisteredComponents, len(components))
	componentsCapabilities := u.getComponentsCapabilitiesFn()
	for i, comp := range components {
		registeredComponents[i] = &runtimev1pb.RegisteredComponents{
			Name:         comp.Name,
			Version:      comp.Spec.Version,
			Type:         comp.Spec.Type,
			Capabilities: metadataGetOrDefaultCapabilities(componentsCapabilities, comp.Name),
		}
	}

	// Subscriptions
	subscriptions := u.compStore.ListSubscriptions()
	ps := make([]*runtimev1pb.PubsubSubscription, len(subscriptions))
	for i, s := range subscriptions {
		ps[i] = &runtimev1pb.PubsubSubscription{
			PubsubName:      s.PubsubName,
			Topic:           s.Topic,
			Metadata:        s.Metadata,
			DeadLetterTopic: s.DeadLetterTopic,
			Rules:           metadataConvertPubSubSubscriptionRules(s.Rules),
		}
	}

	// HTTP endpoints
	endpoints := u.compStore.ListHTTPEndpoints()
	registeredHTTPEndpoints := make([]*runtimev1pb.MetadataHTTPEndpoint, len(endpoints))
	for i, e := range endpoints {
		registeredHTTPEndpoints[i] = &runtimev1pb.MetadataHTTPEndpoint{
			Name: e.Name,
		}
	}

	return &runtimev1pb.GetMetadataResponse{
		Id:                      u.appID,
		ExtendedMetadata:        extendedMetadata,
		RegisteredComponents:    registeredComponents,
		ActiveActorsCount:       activeActorsCount,
		Subscriptions:           ps,
		HttpEndpoints:           registeredHTTPEndpoints,
		AppConnectionProperties: appConnectionProperties,
		RuntimeVersion:          buildinfo.Version(),
		EnabledFeatures:         u.globalConfig.EnabledFeatures(),
	}, nil
}

// SetMetadata Sets value in extended metadata of the sidecar.
func (u *Universal) SetMetadata(ctx context.Context, in *runtimev1pb.SetMetadataRequest) (*emptypb.Empty, error) {
	// Nop if the key is empty
	if in.Key == "" {
		return &emptypb.Empty{}, nil
	}

	u.extendedMetadataLock.Lock()
	if u.extendedMetadata == nil {
		u.extendedMetadata = make(map[string]string)
	}
	u.extendedMetadata[in.Key] = in.Value
	u.extendedMetadataLock.Unlock()

	return &emptypb.Empty{}, nil
}

func metadataGetOrDefaultCapabilities(dict map[string][]string, key string) []string {
	if val, ok := dict[key]; ok {
		return val
	}
	return make([]string, 0)
}

func metadataConvertPubSubSubscriptionRules(rules []*runtimePubsub.Rule) *runtimev1pb.PubsubSubscriptionRules {
	out := &runtimev1pb.PubsubSubscriptionRules{
		Rules: make([]*runtimev1pb.PubsubSubscriptionRule, 0),
	}
	for _, r := range rules {
		out.Rules = append(out.Rules, &runtimev1pb.PubsubSubscriptionRule{
			// TODO avoid using fmt.Sprintf
			Match: fmt.Sprintf("%s", r.Match),
			Path:  r.Path,
		})
	}
	return out
}
