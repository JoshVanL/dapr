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
	"flag"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"k8s.io/client-go/util/homedir"

	"github.com/dapr/dapr/pkg/buildinfo"
	"github.com/dapr/dapr/pkg/health"
	"github.com/dapr/dapr/pkg/metrics"
	"github.com/dapr/dapr/pkg/sentry"
	"github.com/dapr/dapr/pkg/sentry/config"
	"github.com/dapr/dapr/pkg/signals"
	"github.com/dapr/dapr/utils"
	"github.com/dapr/kit/fswatcher"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.sentry")

//nolint:gosec
const (
	defaultCredentialsPath = "/var/run/dapr/credentials"
	// defaultDaprSystemConfigName is the default resource object name for Dapr System Config.
	defaultDaprSystemConfigName = "daprsystem"

	healthzPort = 8080
)

func main() {
	configName := flag.String("config", defaultDaprSystemConfigName, "Path to config file, or name of a configuration object")
	credsPath := flag.String("issuer-credentials", defaultCredentialsPath, "Path to the credentials directory holding the issuer data")
	trustAnchorPath := flag.String("issuer-ca-filename", "ca.crt", "Certificate Authority certificate filename")
	issuerCertPath := flag.String("issuer-certificate-filename", "issuer.crt", "Issuer certificate filename")
	issuerKeyPath := flag.String("issuer-key-filename", "issuer.key", "Issuer private key filename")
	trustDomain := flag.String("trust-domain", "localhost", "The CA trust domain")
	tokenAudience := flag.String("token-audience", "", "Expected audience for tokens; multiple values can be separated by a comma")

	loggerOptions := logger.DefaultOptions()
	loggerOptions.AttachCmdFlags(flag.StringVar, flag.BoolVar)

	metricsExporter := metrics.NewExporter(metrics.DefaultMetricNamespace)
	metricsExporter.Options().AttachCmdFlags(flag.StringVar, flag.BoolVar)

	var kubeconfig *string
	if home := homedir.HomeDir(); home != "" {
		kubeconfig = flag.String("kubeconfig", filepath.Join(home, ".kube", "config"), "(optional) absolute path to the kubeconfig file")
	} else {
		kubeconfig = flag.String("kubeconfig", "", "absolute path to the kubeconfig file")
	}
	flag.Parse()
	if err := utils.SetEnvVariables(map[string]string{
		utils.KubeConfigVar: *kubeconfig,
	}); err != nil {
		log.Fatalf("error set env failed:  %s", err.Error())
	}

	// Apply options to all loggers
	if err := logger.ApplyOptionsToLoggers(&loggerOptions); err != nil {
		log.Fatal(err)
	}

	log.Infof("starting sentry certificate authority -- version %s -- commit %s", buildinfo.Version(), buildinfo.Commit())
	log.Infof("log level set to: %s", loggerOptions.OutputLevel)

	// Initialize dapr metrics exporter
	if err := metricsExporter.Init(); err != nil {
		log.Fatal(err)
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	ctx := signals.Context()
	config, err := config.FromConfigName(*configName)
	if err != nil {
		log.Warn(err)
	}
	config.IssuerCertPath = filepath.Join(*credsPath, *issuerCertPath)
	config.IssuerKeyPath = filepath.Join(*credsPath, *issuerKeyPath)
	config.RootCertPath = filepath.Join(*credsPath, *trustAnchorPath)
	config.TrustDomain = *trustDomain
	if *tokenAudience != "" {
		config.TokenAudience = tokenAudience
	}

	watchDir := filepath.Dir(config.IssuerCertPath)

	log.Infof("starting watch on filesystem directory: %s", watchDir)

	issuerEvent := make(chan struct{})

	go func() {
		for {
			var wg sync.WaitGroup
			wg.Add(2)
			ctx, cancel := context.WithCancel(ctx)
			go func() {
				<-issuerEvent
				cancel()
			}()
			go func() {
				if err := sentry.NewSentryCA(config).Start(ctx); err != nil {
					log.Errorf("error running sentry: %s", err)
				}
			}()
		}
	}()

	// Start the health server in background
	go func() {
		healthzServer := health.NewServer(log)
		healthzServer.Ready()

		if innerErr := healthzServer.Run(ctx, healthzPort); innerErr != nil {
			log.Fatalf("failed to start healthz server: %s", innerErr)
		}
	}()

	// Watch for changes in the watchDir
	// This also blocks until ctx is canceled
	fswatcher.Watch(ctx, watchDir, issuerEvent)

	shutdownDuration := 5 * time.Second
	log.Infof("allowing %s for graceful shutdown to complete", shutdownDuration)
	<-time.After(shutdownDuration)
}
