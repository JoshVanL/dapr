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

package sentry

import (
	"context"
	"crypto"
	"crypto/x509"
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/dapr/kit/logger"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	configurationapi "github.com/dapr/dapr/pkg/apis/configuration/v1alpha1"
	cldapr "github.com/dapr/dapr/pkg/client/clientset/versioned"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/dapr/pkg/sentry/config"
	"github.com/dapr/dapr/pkg/sentry/monitoring"
	"github.com/dapr/dapr/pkg/sentry/server"
	"github.com/dapr/dapr/pkg/sentry/server/ca"
	"github.com/dapr/dapr/pkg/sentry/server/validator"
	valkube "github.com/dapr/dapr/pkg/sentry/server/validator/kubernetes"
	"github.com/dapr/dapr/pkg/sentry/server/validator/selfhosted"
)

var log = logger.NewLogger("dapr.sentry")

func init() {
	if err := configurationapi.AddToScheme(runtime.NewScheme()); err != nil {
		panic(err)
	}
}

// CertificateAuthority is the interface for the Sentry Certificate Authority.
// Starts the Sentry gRPC server and signs workload certificates.
type CertificateAuthority interface {
	Start(context.Context) error
}

type sentry struct {
	conf    config.Config
	running atomic.Bool
}

// New returns a new Sentry Certificate Authority instance.
func New(conf config.Config) CertificateAuthority {
	return &sentry{
		conf: conf,
	}
}

// Start the server in background.
func (s *sentry) Start(ctx context.Context) error {
	// If the server is already running, return an error
	if !s.running.CompareAndSwap(false, true) {
		return errors.New("CertificateAuthority server is already running")
	}

	ns := security.CurrentNamespace()

	camngr, err := ca.New(ctx, s.conf)
	if err != nil {
		return fmt.Errorf("error creating CA: %s", err)
	}
	log.Info("ca certificate key pair ready")

	provider, err := security.New(security.Options{
		ControlPlaneTrustDomain: s.conf.TrustDomain,
		ControlPlaneNamespace:   ns,
		AppID:                   "dapr-sentry",
		TrustAnchors:            camngr.TrustAnchors(),
		MTLSEnabled:             true,
		// Override the request source to our in memory CA since _we_ are sentry!
		OverrideCertRequestSource: func(ctx context.Context, csrDER []byte) ([]*x509.Certificate, error) {
			csr, err := x509.ParseCertificateRequest(csrDER)
			if err != nil {
				monitoring.ServerCertIssueFailed("invalid_csr")
				return nil, err
			}
			certs, err := camngr.SignIdentity(ctx, &ca.SignRequest{
				PublicKey:          csr.PublicKey.(crypto.PublicKey),
				SignatureAlgorithm: csr.SignatureAlgorithm,
				TrustDomain:        s.conf.TrustDomain,
				Namespace:          ns,
				AppID:              "dapr-sentry",
			})
			if err != nil {
				monitoring.ServerCertIssueFailed("ca_error")
				return nil, err
			}
			return certs, nil
		},
	})
	if err != nil {
		return fmt.Errorf("error creating security: %s", err)
	}

	val, err := s.validator()
	if err != nil {
		return err
	}

	log.Info("Validator created, starting sentry server")

	providerErr := make(chan error, 1)
	go func() {
		log.Info("starting security provider")
		providerErr <- provider.Start(ctx)
	}()

	sec, err := provider.Security(ctx)
	if err != nil {
		return errors.Join(err, <-providerErr)
	}

	log.Info("Security ready")

	return errors.Join(server.Start(ctx, server.Options{
		Port:      s.conf.Port,
		Security:  sec,
		Validator: val,
		CA:        camngr,
	}), <-providerErr)
}

func (s *sentry) validator() (validator.Interface, error) {
	if config.IsKubernetesHosted() {
		// We're in Kubernetes, create client and init a new serviceaccount token
		// validator
		config, err := rest.InClusterConfig()
		if err != nil {
			return nil, err
		}
		kubeClient, err := kubernetes.NewForConfig(config)
		if err != nil {
			return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
		}
		daprClient, err := cldapr.NewForConfig(config)
		if err != nil {
			return nil, fmt.Errorf("failed to create fapr client: %w", err)
		}
		sentryID, err := security.SentryID(s.conf.TrustDomain, security.CurrentNamespace())
		if err != nil {
			return nil, err
		}

		// TODO: Remove once the NoDefaultTokenAudience feature is finalized
		noDefaultTokenAudience := false
		return valkube.New(kubeClient, daprClient, sentryID, noDefaultTokenAudience), nil
	}

	return selfhosted.New(), nil
}
