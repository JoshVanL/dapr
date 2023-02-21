/*
Copyright 2021 The Dapr Authors
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

//nolint:forbidigo
package runtime

import (
	"context"
	"os"
	"strings"

	"github.com/dapr/dapr/pkg/acl"
	daprGlobalConfig "github.com/dapr/dapr/pkg/config"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/modes"
	"github.com/dapr/dapr/pkg/operator/client"
	operatorV1 "github.com/dapr/dapr/pkg/proto/operator/v1"
	resiliencyConfig "github.com/dapr/dapr/pkg/resiliency"
)

func FromFlags(ctx context.Context, opts NewRuntimeConfigOpts) (*DaprRuntime, error) {
	runtimeConfig := NewRuntimeConfig(opts)

	var globalConfig *daprGlobalConfig.Configuration
	var configErr error

	// Config and resiliency need the operator client
	var operatorClient operatorV1.OperatorClient
	if opts.Mode == string(modes.KubernetesMode) {
		log.Info("Initializing the operator client")
		client, conn, clientErr := client.GetOperatorClient(ctx, opts.ControlPlaneAddress, runtimeConfig.Security)
		if clientErr != nil {
			return nil, clientErr
		}
		defer conn.Close()
		operatorClient = client
	}

	podName := os.Getenv("POD_NAME")

	if opts.ConfigPath != "" {
		switch modes.DaprMode(opts.Mode) {
		case modes.KubernetesMode:
			log.Debug("Loading Kubernetes config resource: " + opts.ConfigPath)
			globalConfig, configErr = daprGlobalConfig.LoadKubernetesConfiguration(opts.ConfigPath, opts.Namespace, podName, operatorClient)
		case modes.StandaloneMode:
			log.Debug("Loading config from file: " + opts.ConfigPath)
			globalConfig, _, configErr = daprGlobalConfig.LoadStandaloneConfiguration(opts.ConfigPath)
		}
	}

	if configErr != nil {
		log.Fatalf("error loading configuration: %s", configErr)
	}
	if globalConfig == nil {
		log.Info("loading default configuration")
		globalConfig = daprGlobalConfig.LoadDefaultConfiguration()
	}

	globalConfig.LoadFeatures()
	if enabledFeatures := globalConfig.EnabledFeatures(); len(enabledFeatures) > 0 {
		log.Info("Enabled features: " + strings.Join(enabledFeatures, " "))
	}

	// TODO: Remove once AppHealthCheck feature is finalized
	if !globalConfig.IsFeatureEnabled(daprGlobalConfig.AppHealthCheck) && opts.EnableAppHealthCheck {
		log.Warnf("App health checks are a preview feature and require the %s feature flag to be enabled. See https://docs.dapr.io/operations/configuration/preview-features/ on how to enable preview features.", daprGlobalConfig.AppHealthCheck)
		runtimeConfig.AppHealthCheck = nil
	}

	// Initialize metrics only if MetricSpec is enabled.
	if globalConfig.Spec.MetricSpec.Enabled {
		if mErr := diag.InitMetrics(runtimeConfig.ID, opts.Namespace, globalConfig.Spec.MetricSpec.Rules); mErr != nil {
			log.Errorf(NewInitError(InitFailure, "metrics", mErr).Error())
		}
	}

	// Load Resiliency
	var resiliencyProvider *resiliencyConfig.Resiliency
	switch modes.DaprMode(opts.Mode) {
	case modes.KubernetesMode:
		resiliencyConfigs := resiliencyConfig.LoadKubernetesResiliency(log, opts.ID, opts.Namespace, operatorClient)
		log.Debugf("Found %d resiliency configurations from Kubernetes", len(resiliencyConfigs))
		resiliencyProvider = resiliencyConfig.FromConfigurations(log, resiliencyConfigs...)
	case modes.StandaloneMode:
		if len(opts.ResourcesPath) > 0 {
			resiliencyConfigs := resiliencyConfig.LoadLocalResiliency(log, opts.ID, opts.ResourcesPath...)
			log.Debugf("Found %d resiliency configurations in resources path", len(resiliencyConfigs))
			resiliencyProvider = resiliencyConfig.FromConfigurations(log, resiliencyConfigs...)
		} else {
			resiliencyProvider = resiliencyConfig.FromConfigurations(log)
		}
	}
	log.Info("Resiliency configuration loaded")

	accessControlList, err := acl.ParseAccessControlSpec(globalConfig.Spec.AccessControlSpec, string(runtimeConfig.ApplicationProtocol))
	if err != nil {
		log.Fatalf(err.Error())
	}

	return NewDaprRuntime(runtimeConfig, globalConfig, accessControlList, resiliencyProvider), nil
}
