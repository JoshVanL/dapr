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

package options

import (
	"k8s.io/client-go/rest"

	"github.com/dapr/dapr/pkg/sentry/trust/internal"
	distributorkube "github.com/dapr/dapr/pkg/sentry/trust/internal/distributor/kube"
	sourcekube "github.com/dapr/dapr/pkg/sentry/trust/internal/source/kube"
)

type sourceKind string
type distributionKind string

const (
	// SourceKindKubernetesConfigMap indicates that the trust anchors are
	// stored in a Kubernetes ConfigMap.
	SourceKindKubernetesConfigMap sourceKind = "ConfigMap"

	// SourceKindKubernetesSecret indicates that the trust anchors are
	// stored in a Kubernetes Secret.
	SourceKindKubernetesSecret = "Secret"

	// DistributionKindKubernetesConfigMap indicates that the trust anchors
	// are distributed to Kubernetes ConfigMaps.
	DistributionKindKubernetesConfigMap distributionKind = "ConfigMap"
)

type Options struct {
	BuilderSource      BuilderSource
	BuilderDistributor BuilderDistributor
}

type BuilderSource interface {
	Build() (internal.Source, error)
}

type BuilderDistributor interface {
	Build(source internal.Source) (internal.Distributor, error)
}

// Kube is the options for Kubernetes.
type Kube struct {
	Namespace  string
	RestConfig *rest.Config
}

type SourceKube struct {
	Kube

	SourceKind   sourceKind
	ResourceName string
}

type DistributionKube struct {
	Kube
}

func (o SourceKube) Build() (internal.Source, error) {
	return sourcekube.New(sourcekube.Options{
		RestConfig:   o.RestConfig,
		Namespace:    o.Namespace,
		ResourceName: o.ResourceName,
		SourceKind:   sourcekube.SourceKind(o.SourceKind),
	})
}

func (o DistributionKube) Build(source internal.Source) (internal.Distributor, error) {
	return distributorkube.New(distributorkube.Options{
		RestConfig: o.RestConfig,
		Namespace:  o.Namespace,
		Source:     source,
	})
}
