package security

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	middleware "github.com/grpc-ecosystem/go-grpc-middleware"
	retry "github.com/grpc-ecosystem/go-grpc-middleware/retry"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	diag "github.com/dapr/dapr/pkg/diagnostics"
	sentryv1pb "github.com/dapr/dapr/pkg/proto/sentry/v1"
)

const (
	sentrySignTimeout = time.Second * 5
	certType          = "CERTIFICATE"
	kubeTknPath       = "/var/run/secrets/dapr.io/sentrytoken/token"
	legacyKubeTknPath = "/var/run/secrets/kubernetes.io/serviceaccount/token"
	sentryMaxRetries  = 100
)

type Authenticator interface {
	TrustAnchors() []byte
	SentryTrustDomain() string
	TLSServerConfig() *tls.Config
	GRPCDialOption(serverName string) grpc.DialOption
	GRPCServerOption() grpc.ServerOption
}

type Options struct {
	SentryAddress     string
	SentryTrustDomain string
	TrustAnchors      []byte
	Namespace         string
	AppID             string
}

type authenticator struct {
	sentryAddress     string
	sentryTrustDomain string
	trustAnchors      *x509.CertPool
	trustAnchorsPEM   []byte
	namespace         string
	appID             string
	genCSRFunc        func(id string) ([]byte, []byte, error)
	currentSignedCert *signedCertificate
	currentExpiry     time.Time
	lock              sync.RWMutex
}

type signedCertificate struct {
	cert         *tls.Certificate
	trustAnchors *x509.CertPool
}

func newAuthenticator(ctx context.Context, opts Options, genCSRFunc func(id string) ([]byte, []byte, error)) (Authenticator, error) {
	trustAnchors := x509.NewCertPool()
	if !trustAnchors.AppendCertsFromPEM(opts.TrustAnchors) {
		return nil, errors.New("failed to parse trust anchors")
	}
	a := &authenticator{
		trustAnchors:      trustAnchors,
		trustAnchorsPEM:   opts.TrustAnchors,
		sentryAddress:     opts.SentryAddress,
		sentryTrustDomain: opts.SentryTrustDomain,
		namespace:         opts.Namespace,
		appID:             opts.AppID,
		genCSRFunc:        genCSRFunc,
	}

	if err := a.renewIdentiyCertificate(ctx); err != nil {
		return nil, fmt.Errorf("failed to fetch initial identity certificate: %s", err)
	}

	go a.startWorkloadCertRotation(ctx)

	return a, nil
}

// TrustAnchors returns the extracted root certificates that serves as the
// trust anchors.
func (a *authenticator) TrustAnchors() []byte {
	return a.trustAnchorsPEM
}

// SentryTrustDomain returns the trust domain of the sentry server.
func (a *authenticator) SentryTrustDomain() string {
	return a.sentryTrustDomain
}

// GRPCDialOption returns a gRPC dial option which instruments client
// authentication using the current signed client certificate.
func (a *authenticator) GRPCDialOption(serverName string) grpc.DialOption {
	return grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{
		RootCAs:    a.currentSignedCert.trustAnchors.Clone(),
		ServerName: serverName,
		GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
			a.lock.RLock()
			defer a.lock.RUnlock()
			return a.currentSignedCert.cert, nil
		},
		ClientCAs: a.currentSignedCert.trustAnchors.Clone(),
		// TODO: add proper SPIFFE mTLS validation.
		//VerifyPeerCertificate: nil,
	}))
}

// TLSServerConfig returns a TLS server config which instruments using the
// current signed server certificate.
func (a *authenticator) TLSServerConfig() *tls.Config {
	return &tls.Config{
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			a.lock.RLock()
			defer a.lock.RUnlock()
			return a.currentSignedCert.cert, nil
		},
	}
}

// GRPCServerOption returns a gRPC server option which instruments
// authentication of clients using the current trust anchors.
func (a *authenticator) GRPCServerOption() grpc.ServerOption {
	a.lock.RLock()
	defer a.lock.RUnlock()

	tlsConfig := a.TLSServerConfig().Clone()
	tlsConfig.ClientAuth = tls.RequireAndVerifyClientCert
	tlsConfig.RootCAs = a.currentSignedCert.trustAnchors.Clone()
	tlsConfig.ClientCAs = a.currentSignedCert.trustAnchors.Clone()
	// TODO: add proper SPIFFE mTLS validation.
	//VerifyPeerCertificate: nil,

	return grpc.Creds(credentials.NewTLS(tlsConfig))
}

// renewIdentityCertificate renews the identity certificate.
func (a *authenticator) renewIdentiyCertificate(ctx context.Context) error {
	csrb, pk, err := a.genCSRFunc(a.appID)
	if err != nil {
		return err
	}
	certPem := pem.EncodeToMemory(&pem.Block{Type: certType, Bytes: csrb})

	unaryClientInterceptor := retry.UnaryClientInterceptor()

	if diag.DefaultGRPCMonitoring.IsEnabled() {
		unaryClientInterceptor = middleware.ChainUnaryClient(
			unaryClientInterceptor,
			diag.DefaultGRPCMonitoring.UnaryClientInterceptor(),
		)
	}

	conn, err := grpc.DialContext(ctx,
		a.sentryAddress,
		grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{
			RootCAs:    a.trustAnchors,
			ServerName: a.sentryTrustDomain,
		})),
		grpc.WithUnaryInterceptor(unaryClientInterceptor))
	if err != nil {
		diag.DefaultMonitoring.MTLSWorkLoadCertRotationFailed("sentry_conn")
		return fmt.Errorf("error establishing connection to sentry: %w", err)
	}
	defer conn.Close()

	c := sentryv1pb.NewCAClient(conn)

	resp, err := c.SignCertificate(ctx,
		&sentryv1pb.SignCertificateRequest{
			CertificateSigningRequest: certPem,
			Id:                        getSentryIdentifier(a.appID),
			Token:                     getToken(),
			TrustDomain:               a.sentryTrustDomain,
			Namespace:                 a.namespace,
		}, retry.WithMax(sentryMaxRetries), retry.WithPerRetryTimeout(sentrySignTimeout))
	if err != nil {
		diag.DefaultMonitoring.MTLSWorkLoadCertRotationFailed("sign")
		return fmt.Errorf("error from sentry SignCertificate: %w", err)
	}

	workloadCert := resp.GetWorkloadCertificate()
	validTimestamp := resp.GetValidUntil()
	if err = validTimestamp.CheckValid(); err != nil {
		diag.DefaultMonitoring.MTLSWorkLoadCertRotationFailed("invalid_ts")
		return fmt.Errorf("error parsing ValidUntil: %w", err)
	}

	cert, err := tls.X509KeyPair(workloadCert, pk)
	if err != nil {
		return fmt.Errorf("error parsing newly signed certificate: %w", err)
	}

	a.lock.Lock()
	defer a.lock.Unlock()
	a.currentSignedCert = &signedCertificate{
		cert:         &cert,
		trustAnchors: a.trustAnchors.Clone(),
	}
	a.currentExpiry = validTimestamp.AsTime()
	return nil
}

// currently we support Kubernetes identities.
func getToken() string {
	b, err := os.ReadFile(kubeTknPath)
	if err != nil && os.IsNotExist(err) {
		// Attempt to use the legacy token if that exists
		b, _ = os.ReadFile(legacyKubeTknPath)
		if len(b) > 0 {
			log.Warn("⚠️ daprd is initializing using the legacy service account token with access to Kubernetes APIs, which is discouraged. This usually happens when daprd is running against an older version of the Dapr control plane.")
		}
	}
	return string(b)
}

func getSentryIdentifier(appID string) string {
	// return injected identity, default id if not present
	localID := os.Getenv("SENTRY_LOCAL_IDENTITY")
	if localID != "" {
		return localID
	}
	return appID
}

func (a *authenticator) startWorkloadCertRotation(ctx context.Context) {
	defer log.Debug("stopping workload cert expiry watcher")
	log.Infof("starting workload cert expiry watcher. current cert expires on: %s", a.currentExpiry.String())

	for {
		// Renewal time is 70% through the certificate duration period.
		renewalTime := a.currentExpiry.Add(-time.Duration(0.7 * float64(a.currentExpiry.Sub(time.Now()))))
		select {
		case <-time.After(time.Until(renewalTime)):
			log.Infof("renewing workload cert. current cert expires on: %s", a.currentExpiry.String())
			if err := a.renewIdentiyCertificate(ctx); err != nil {
				log.Errorf("error renewing identity certificate, trying again in 10 seconds: %s", err)
				select {
				case <-time.After(10 * time.Second):
					continue
				case <-ctx.Done():
					return
				}
			}
			log.Infof("successfully renewed workload cert. new cert expires on: %s", a.currentExpiry.String())

		case <-ctx.Done():
			return
		}
	}
}
