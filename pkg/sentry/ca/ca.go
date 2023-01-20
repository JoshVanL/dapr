package ca

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/dapr/pkg/sentry/config"
	"github.com/dapr/dapr/pkg/sentry/monitoring"
	"github.com/dapr/kit/logger"
	"github.com/spiffe/go-spiffe/v2/spiffeid"
)

const (
	// TrustBundleK8sName is the name of the kubernetes secret that holds the
	// issuer certificate key pair and trust anchors, and configmap that holds
	// the trust anchors.
	TrustBundleK8sName = "dapr-trust-bundle" /* #nosec */
)

var log = logger.NewLogger("dapr.sentry.ca")

// SignRequest signs a certificate request with the issuer certificate.
type SignRequest struct {
	// Public key of the certificate request.
	PublicKey crypto.PublicKey

	// Signature of the certificate request.
	SignatureAlgorithm x509.SignatureAlgorithm

	// TrustDomain is the trust domain of the client.
	TrustDomain string

	// Namespace is the namespace of the client.
	Namespace string

	// AppID is the app id of the client.
	AppID string

	// Optional DNS names to add to the certificate.
	DNS []string
}

// Interface is the interface for the CA.
type Interface interface {
	SignIdentity(context.Context, *SignRequest) ([]*x509.Certificate, error)
	TrustAnchors() []byte
}

type store interface {
	store(context.Context, caBundle) error
	get(context.Context) (caBundle, bool, error)
}

// ca is the implementation of the CA interface.
type ca struct {
	bundle caBundle
	config config.Config
}

type caBundle struct {
	trustAnchors []byte
	issChainPEM  []byte
	issKeyPEM    []byte
	issChain     []*x509.Certificate
	issKey       any
}

func New(ctx context.Context, conf config.Config) (Interface, error) {
	var store store
	if config.IsKubernetesHosted() {
		log.Info("using kubernetes secret store for trust bundle storage")

		restConfig, err := rest.InClusterConfig()
		if err != nil {
			return nil, err
		}
		client, err := kubernetes.NewForConfig(restConfig)
		if err != nil {
			return nil, err
		}

		store = &kube{
			config:    conf,
			namespace: security.CurrentNamespace(),
			client:    client,
		}
	} else {
		log.Info("using local file system for trust bundle storage")
		store = &selfhosted{config: conf}
	}

	bundle, ok, err := store.get(ctx)
	if err != nil {
		return nil, err
	}

	if !ok {
		log.Info("root and issuer certs not found: generating self signed CA")

		bundle, err = generateCABundle(conf)
		if err != nil {
			return nil, err
		}

		log.Info("root and issuer certs generated")

		if err := store.store(ctx, bundle); err != nil {
			return nil, err
		}

		log.Info("self signed certs generated and persisted successfully")
		monitoring.IssuerCertChanged(ctx)
		monitoring.IssuerCertExpiry(ctx, bundle.issChain[0].NotAfter)
	}

	return &ca{
		bundle: bundle,
		config: conf,
	}, nil
}

func (c *ca) SignIdentity(ctx context.Context, req *SignRequest) ([]*x509.Certificate, error) {
	td, err := spiffeid.TrustDomainFromString(req.TrustDomain)
	if err != nil {
		return nil, err
	}

	spiffeID, err := spiffeid.FromPathf(td, "/ns/%s/%s", req.Namespace, req.AppID)
	if err != nil {
		return nil, err
	}

	tmpl, err := generateWorkloadCert(req.SignatureAlgorithm, c.config.WorkloadCertTTL, c.config.AllowedClockSkew, spiffeID)
	if err != nil {
		return nil, err
	}
	tmpl.DNSNames = append(tmpl.DNSNames, req.DNS...)

	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, c.bundle.issChain[0], req.PublicKey, c.bundle.issKey)
	if err != nil {
		return nil, err
	}

	cert, err := x509.ParseCertificate(certDER)
	if err != nil {
		return nil, err
	}

	return append([]*x509.Certificate{cert}, c.bundle.issChain...), nil
}

func (c *ca) TrustAnchors() []byte {
	return c.bundle.trustAnchors
}

func generateCABundle(conf config.Config) (caBundle, error) {
	const caTTL = 10 * 365 * 24 * time.Hour

	rootKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return caBundle{}, err
	}

	rootCert, err := generateRootCert(conf.AllowedClockSkew)
	if err != nil {
		return caBundle{}, fmt.Errorf("failed to generate root cert: %w", err)
	}

	rootCertDER, err := x509.CreateCertificate(rand.Reader, rootCert, rootCert, &rootKey.PublicKey, rootKey)
	if err != nil {
		return caBundle{}, fmt.Errorf("failed to sign root certificate: %w", err)
	}
	trustAnchors := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: rootCertDER})

	issKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return caBundle{}, err
	}
	issKeyDer, err := x509.MarshalECPrivateKey(issKey)
	if err != nil {
		return caBundle{}, err
	}
	issKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: issKeyDer})

	issCert, err := generateIssuerCert(conf.AllowedClockSkew)
	if err != nil {
		return caBundle{}, fmt.Errorf("failed to generate issuer cert: %w", err)
	}
	issCertDER, err := x509.CreateCertificate(rand.Reader, issCert, rootCert, &issKey.PublicKey, rootKey)
	if err != nil {
		return caBundle{}, fmt.Errorf("failed to sign issuer cert: %w", err)
	}
	issCertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: issCertDER})

	issCert, err = x509.ParseCertificate(issCertDER)
	if err != nil {
		return caBundle{}, err
	}

	return caBundle{
		trustAnchors: trustAnchors,
		issChainPEM:  issCertPEM,
		issKeyPEM:    issKeyPEM,
		issChain:     []*x509.Certificate{issCert},
		issKey:       issKey,
	}, nil
}
