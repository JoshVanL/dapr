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
	"encoding/base64"
	"flag"
	"fmt"
	"path/filepath"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/homedir"

	"github.com/dapr/dapr/pkg/buildinfo"
	scheme "github.com/dapr/dapr/pkg/client/clientset/versioned"
	"github.com/dapr/dapr/pkg/concurrency"
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
	daprClient, err := scheme.NewForConfig(conf)
	if err != nil {
		log.Fatalf("error creating dapr client: %s", err)
	}
	uids, err := injector.AllowedControllersServiceAccountUID(ctx, cfg, kubeClient)
	if err != nil {
		log.Fatalf("failed to get authentication uids from services accounts: %s", err)
	}

	secProvider, err := security.New(ctx, security.Options{
		SentryAddress:           cfg.SentryAddress,
		ControlPlaneTrustDomain: cfg.ControlPlaneTrustDomain,
		ControlPlaneNamespace:   security.CurrentNamespace(),
		TrustAnchorsFile:        cfg.TrustAnchorsFile,
		AppID:                   "dapr-injector",
		MTLSEnabled:             true,
	})
	if err != nil {
		log.Fatal(err)
	}

	inj, err := injector.NewInjector(secProvider, uids, cfg, daprClient, kubeClient)
	if err != nil {
		log.Fatalf("error creating injector: %s", err)
	}

	healthzServer := health.NewServer(log)
	caBundleCh := make(chan []byte)
	mngr := concurrency.NewRunnerManager(
		secProvider.Start,
		inj.Run,
		func(ctx context.Context) error {
			if err := inj.Ready(ctx); err != nil {
				return err
			}
			healthzServer.Ready()
			<-ctx.Done()
			return nil
		},
		func(ctx context.Context) error {
			if err := healthzServer.Run(ctx, healthzPort); err != nil {
				return fmt.Errorf("failed to start healthz server: %w", err)
			}
			return nil
		},
		func(ctx context.Context) error {
			sec, rErr := secProvider.Security(ctx)
			if rErr != nil {
				return rErr
			}
			sec.WatchTrustAnchors(ctx, caBundleCh)
			return nil
		},
		func(ctx context.Context) error {
			sec, err := secProvider.Security(ctx)
			if err != nil {
				return err
			}

			caBundle, rErr := sec.CurrentTrustAnchors()
			if rErr != nil {
				return rErr
			}

			for {
				_, rErr = kubeClient.AdmissionregistrationV1().MutatingWebhookConfigurations().Patch(ctx,
					"dapr-sidecar-injector",
					types.JSONPatchType,
					[]byte(`[{"op":"replace","path":"/webhooks/0/clientConfig/caBundle","value":"`+base64.StdEncoding.EncodeToString(caBundle)+`"}]`),
					metav1.PatchOptions{},
				)
				if rErr != nil {
					return rErr
				}

				select {
				case caBundle = <-caBundleCh:
				case <-ctx.Done():
					return nil
				}
			}
		},
	)

	if err := mngr.Run(ctx); err != nil {
		log.Fatalf("error running injector: %s", err)
	}

	log.Infof("Dapr sidecar injector shut down gracefully")
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

	depRCF := flag.String("issuer-ca-filename", "", "DEPRECATED")
	depICF := flag.String("issuer-certificate-filename", "", "DEPRECATED")
	depIKF := flag.String("issuer-key-filename", "", "DEPRECATED")

	flag.Parse()

	if len(*depRCF) > 0 || len(*depICF) > 0 || len(*depIKF) > 0 {
		log.Warn("issuer-ca-filename, issuer-certificate-filename and issuer-key-filename are deprecated and will be removed in v1.12. Please use certchain instead.")
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
