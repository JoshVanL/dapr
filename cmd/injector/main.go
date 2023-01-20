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
	"flag"
	"os"
	"path/filepath"

	"k8s.io/client-go/util/homedir"

	"github.com/dapr/dapr/pkg/buildinfo"
	scheme "github.com/dapr/dapr/pkg/client/clientset/versioned"
	"github.com/dapr/dapr/pkg/health"
	"github.com/dapr/dapr/pkg/injector"
	"github.com/dapr/dapr/pkg/injector/monitoring"
	"github.com/dapr/dapr/pkg/metrics"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/dapr/pkg/signals"
	"github.com/dapr/dapr/utils"
	"github.com/dapr/kit/logger"
)

var (
	log         = logger.NewLogger("dapr.injector")
	healthzPort int

	controlPlaneTrustDomain string
	sentryAddress           string
	trustAnchorsFile        string
)

func main() {
	log.Infof("starting Dapr Sidecar Injector -- version %s -- commit %s", buildinfo.Version(), buildinfo.Commit())

	ctx := signals.Context()
	cfg, err := injector.GetConfig()
	if err != nil {
		log.Fatalf("error getting config: %s", err)
	}

	kubeClient := utils.GetKubeClient()
	conf := utils.GetConfig()
	daprClient, _ := scheme.NewForConfig(conf)

	healthzServer := health.NewServer(log)
	go func() {
		healthzErr := healthzServer.Run(ctx, healthzPort)
		if healthzErr != nil {
			log.Fatalf("failed to start healthz server: %s", healthzErr)
		}
	}()

	uids, err := injector.AllowedControllersServiceAccountUID(ctx, cfg, kubeClient)
	if err != nil {
		log.Fatalf("failed to get authentication uids from services accounts: %s", err)
	}

	trustAnchors, err := os.ReadFile(trustAnchorsFile)
	if err != nil {
		log.Fatalf("failed to read trust anchor file: %s", err)
	}

	mtlsEnabled, err := injector.MTLSEnabled(daprClient)
	if err != nil {
		log.Fatalf("failed to check if mTLS is enabled: %s", err)
	}

	sec, err := security.New(ctx, security.Options{
		SentryAddress:           sentryAddress,
		ControlPlaneTrustDomain: controlPlaneTrustDomain,
		ControlPlaneNamespace:   security.CurrentNamespace(),
		TrustAnchors:            trustAnchors,
		AppID:                   "dapr-injector",
		AppNamespace:            security.CurrentNamespace(),
		MTLSEnabled:             mtlsEnabled,
	})
	if err != nil {
		log.Fatalf("failed to create security: %s", err)
	}

	inj := injector.NewInjector(uids, cfg, daprClient, kubeClient, sec)

	// Blocking call
	inj.Run(ctx, func() {
		healthzServer.Ready()
	})

	log.Infof("Dapr sidecar injector shut down")
}

func init() {
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

	flag.IntVar(&healthzPort, "healthz-port", 8080, "The port used for health checks")

	// TODO: remove these flags in a future release. They now do nothing and are deprecated.
	issCAKey := flag.String("issuer-ca-secret-key", "", "DEPRECATED: This flag does nothing and will be removed in a future release.")
	issCertKey := flag.String("issuer-certificate-secret-key", "", "DEPRECATED: This flag does nothing and will be removed in a future release.")
	issKey := flag.String("issuer-key-secret-key", "", "DEPRECATED: This flag does nothing and will be removed in a future release.")

	flag.StringVar(&controlPlaneTrustDomain, "control-plane-trust-domain", "cluster.local", "The trust domain of the control plane")
	flag.StringVar(&sentryAddress, "sentry-address", "dapr-sentry.dapr-system.svc:443", "The address of the sentry service")
	flag.StringVar(&trustAnchorsFile, "trust-anchors-file", "/var/run/dapr/sentry/trustAnchors", "The path to the trust anchor file")

	flag.Parse()

	if issCAKey != nil && len(*issCAKey) > 0 {
		log.Warn("The flag issuer-ca-secret-key is deprecated and will be removed in a future release.")
	}
	if issCertKey != nil && len(*issCertKey) > 0 {
		log.Warn("The flag issuer-certificate-secret-key is deprecated and will be removed in a future release.")
	}
	if issKey != nil && len(*issKey) > 0 {
		log.Warn("The flag issuer-key-secret-key is deprecated and will be removed in a future release.")
	}

	if err := utils.SetEnvVariables(map[string]string{
		utils.KubeConfigVar: *kubeconfig,
	}); err != nil {
		log.Fatalf("error set env failed:  %s", err.Error())
	}

	// Apply options to all loggers
	if err := logger.ApplyOptionsToLoggers(&loggerOptions); err != nil {
		log.Fatal(err)
	} else {
		log.Infof("log level set to: %s", loggerOptions.OutputLevel)
	}

	// Initialize dapr metrics exporter
	if err := metricsExporter.Init(); err != nil {
		log.Fatal(err)
	}

	// Initialize injector service metrics
	if err := monitoring.InitMetrics(); err != nil {
		log.Fatal(err)
	}
}
