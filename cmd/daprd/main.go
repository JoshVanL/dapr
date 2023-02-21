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

package main

import (
	"context"

	"go.uber.org/automaxprocs/maxprocs"

	// Register all components
	_ "github.com/dapr/dapr/cmd/daprd/components"

	bindingsLoader "github.com/dapr/dapr/pkg/components/bindings"
	configurationLoader "github.com/dapr/dapr/pkg/components/configuration"
	lockLoader "github.com/dapr/dapr/pkg/components/lock"
	httpMiddlewareLoader "github.com/dapr/dapr/pkg/components/middleware/http"
	nrLoader "github.com/dapr/dapr/pkg/components/nameresolution"
	pubsubLoader "github.com/dapr/dapr/pkg/components/pubsub"
	secretstoresLoader "github.com/dapr/dapr/pkg/components/secretstores"
	stateLoader "github.com/dapr/dapr/pkg/components/state"
	workflowsLoader "github.com/dapr/dapr/pkg/components/workflows"
	"github.com/dapr/dapr/pkg/security"

	"github.com/dapr/dapr/cmd/daprd/options"
	"github.com/dapr/dapr/pkg/runtime"
	"github.com/dapr/kit/logger"
)

var (
	log        = logger.NewLogger("dapr.runtime")
	logContrib = logger.NewLogger("dapr.contrib")
)

func main() {
	// set GOMAXPROCS
	_, _ = maxprocs.Set()

	opts, err := options.Parse()
	if err != nil {
		log.Fatal(err)
	}

	// TODO: Update with a shared context rooted in a signal.
	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()

	secProvider, err := security.New(ctx, security.Options{
		SentryAddress:           opts.SentryAddress,
		ControlPlaneTrustDomain: opts.ControlPlaneTrustDomain,
		ControlPlaneNamespace:   opts.ControlPlaneNamespace,
		TrustAnchors:            opts.TrustAnchors,
		AppID:                   opts.AppID,
		MTLSEnabled:             opts.EnableMTLS,
	})
	if err != nil {
		log.Fatal(err)
	}

	go func() {
		defer cancel()
		if err := secProvider.Start(ctx); err != nil {
			log.Error(err)
		}
	}()

	sec, err := secProvider.Security(ctx)
	if err != nil {
		log.Fatal(err)
	}

	rt, err := runtime.FromFlags(ctx, runtime.NewRuntimeConfigOpts{
		ID:                           opts.AppID,
		PlacementAddresses:           opts.PlacementAddresses,
		ControlPlaneAddress:          opts.ControlPlaneAddress,
		AllowedOrigins:               opts.AllowedOrigins,
		ResourcesPath:                opts.ResourcesPath,
		AppProtocol:                  opts.AppProtocol,
		Mode:                         opts.Mode,
		HTTPPort:                     opts.HTTPPort,
		InternalGRPCPort:             opts.InternalGRPCPort,
		APIGRPCPort:                  opts.APIGRPCPort,
		APIListenAddresses:           opts.APIListenAddresses,
		PublicPort:                   opts.PublicPort,
		AppPort:                      opts.AppPort,
		ProfilePort:                  opts.ProfilePort,
		EnableProfiling:              opts.EnableProfiling,
		MaxConcurrency:               opts.AppMaxConcurrency,
		SentryAddress:                opts.SentryAddress,
		MTLSEnabled:                  opts.EnableMTLS,
		AppSSL:                       opts.AppSSL,
		MaxRequestBodySize:           opts.HTTPMaxRequestBodySize,
		UnixDomainSocket:             opts.UnixDomainSocket,
		ReadBufferSize:               opts.HTTPReadBufferSize,
		GracefulShutdownDuration:     opts.GracefulShutdownDuration,
		EnableAPILogging:             opts.EnableAPILogging,
		DisableBuiltinK8sSecretStore: opts.DisableBuiltinK8sSecretStore,
		EnableAppHealthCheck:         opts.EnableAppHealthCheck,
		AppHealthCheckPath:           opts.AppHealthCheckPath,
		AppHealthProbeInterval:       opts.AppHealthProbeInterval,
		AppHealthProbeTimeout:        opts.AppHealthProbeTimeout,
		AppHealthThreshold:           opts.AppHealthThreshold,
		ConfigPath:                   opts.Config,
		Namespace:                    security.CurrentNamespace(),
		Security:                     sec,
	})
	if err != nil {
		log.Fatal(err)
	}

	secretstoresLoader.DefaultRegistry.Logger = logContrib
	stateLoader.DefaultRegistry.Logger = logContrib
	configurationLoader.DefaultRegistry.Logger = logContrib
	lockLoader.DefaultRegistry.Logger = logContrib
	pubsubLoader.DefaultRegistry.Logger = logContrib
	nrLoader.DefaultRegistry.Logger = logContrib
	bindingsLoader.DefaultRegistry.Logger = logContrib
	workflowsLoader.DefaultRegistry.Logger = logContrib
	httpMiddlewareLoader.DefaultRegistry.Logger = log // Note this uses log on purpose

	stopCh := runtime.ShutdownSignal()

	err = rt.Run(
		runtime.WithSecretStores(secretstoresLoader.DefaultRegistry),
		runtime.WithStates(stateLoader.DefaultRegistry),
		runtime.WithConfigurations(configurationLoader.DefaultRegistry),
		runtime.WithLocks(lockLoader.DefaultRegistry),
		runtime.WithPubSubs(pubsubLoader.DefaultRegistry),
		runtime.WithNameResolutions(nrLoader.DefaultRegistry),
		runtime.WithBindings(bindingsLoader.DefaultRegistry),
		runtime.WithHTTPMiddlewares(httpMiddlewareLoader.DefaultRegistry),
		runtime.WithWorkflowComponents(workflowsLoader.DefaultRegistry),
	)
	if err != nil {
		log.Fatalf("fatal error from runtime: %s", err)
	}

	<-stopCh
	cancel()
	rt.ShutdownWithWait()
}
