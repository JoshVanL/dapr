package sentry

import (
	"context"
	"crypto"
	"crypto/x509"
	"errors"
	"fmt"

	"github.com/dapr/kit/logger"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	configurationapi "github.com/dapr/dapr/pkg/apis/configuration/v1alpha1"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/dapr/pkg/sentry/ca"
	"github.com/dapr/dapr/pkg/sentry/config"
	"github.com/dapr/dapr/pkg/sentry/monitoring"
	"github.com/dapr/dapr/pkg/sentry/server"
	"github.com/dapr/dapr/pkg/sentry/validator"
	valkube "github.com/dapr/dapr/pkg/sentry/validator/kubernetes"
	"github.com/dapr/dapr/pkg/sentry/validator/selfhosted"
)

var log = logger.NewLogger("dapr.sentry")

var scheme = runtime.NewScheme()

func init() {
	if err := configurationapi.AddToScheme(scheme); err != nil {
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
	running chan bool
}

// NewSentryCA returns a new Sentry Certificate Authority instance.
func NewSentryCA(conf config.Config) CertificateAuthority {
	return &sentry{
		running: make(chan bool, 1),
		conf:    conf,
	}
}

// Start the server in background.
func (s *sentry) Start(ctx context.Context) error {
	// If the server is already running, return an error
	select {
	case s.running <- true:
	default:
		return errors.New("CertificateAuthority server is already running")
	}

	ns := security.CurrentNamespace()

	camngr, err := ca.New(ctx, s.conf)
	if err != nil {
		return fmt.Errorf("error creating CA: %s", err)
	}
	log.Info("ca certificate key pair ready")

	sec, err := security.New(ctx, security.Options{
		ControlPlaneTrustDomain: s.conf.TrustDomain,
		ControlPlaneNamespace:   ns,
		AppID:                   "dapr-sentry",
		AppNamespace:            ns,
		TrustAnchors:            camngr.TrustAnchors(),
		MTLSEnabled:             true,
		// Override the request source to our in memory CA since _we_ are sentry!
		OverrideRequestSource: func(ctx context.Context, csrDER []byte) ([]*x509.Certificate, error) {
			csr, err := x509.ParseCertificateRequest(csrDER)
			if err != nil {
				monitoring.ServerCertIssueFailed(ctx, "invalid_csr")
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
				monitoring.ServerCertIssueFailed(ctx, "ca_error")
				return nil, err
			}
			return certs, nil
		},
	})
	if err != nil {
		return fmt.Errorf("error creating security: %s", err)
	}
	log.Info("security ready")

	val, err := s.validator()
	if err != nil {
		return err
	}
	log.Info("validator created")

	return server.Run(ctx, server.Options{
		Port:      s.conf.Port,
		Security:  sec,
		Validator: val,
		CA:        camngr,
	})
}

func (s *sentry) validator() (validator.Interface, error) {
	if config.IsKubernetesHosted() {
		// we're in Kubernetes, create client and init a new serviceaccount token validator
		config, err := rest.InClusterConfig()
		if err != nil {
			return nil, err
		}
		kubeClient, err := kubernetes.NewForConfig(config)
		if err != nil {
			return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
		}
		cclient, err := client.New(config, client.Options{Scheme: scheme})
		if err != nil {
			return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
		}
		// TODO: Remove once the NoDefaultTokenAudience feature is finalized
		noDefaultTokenAudience := false
		return valkube.New(cclient, kubeClient, s.conf.GetTokenAudiences(), noDefaultTokenAudience), nil
	}

	return selfhosted.New(), nil
}
