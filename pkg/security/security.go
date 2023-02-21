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

package security

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"sync/atomic"

	"github.com/spiffe/go-spiffe/v2/spiffegrpc/grpccredentials"
	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/spiffe/go-spiffe/v2/spiffetls/tlsconfig"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"k8s.io/utils/clock"

	"github.com/dapr/dapr/pkg/concurrency"
	"github.com/dapr/dapr/pkg/diagnostics"
	secpem "github.com/dapr/dapr/pkg/security/pem"
	"github.com/dapr/kit/fswatcher"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.security")

type RequestFn func(ctx context.Context, der []byte) ([]*x509.Certificate, error)

// TODO: add server option for using ACL to validate trust domain.

// Interface implements middleware for client and server connection security.
type Interface interface {
	// TODO!! @joshvanl: verify all of these are needed.
	ControlPlaneTrustDomain() spiffeid.TrustDomain
	ControlPlaneNamespace() string
	CurrentTrustAnchors() ([]byte, error)

	TLSServerConfigMTLS(spiffeid.TrustDomain) (*tls.Config, error)
	TLSServerConfigBasicTLS() *tls.Config
	TLSServerConfigBasicTLSOption(*tls.Config)

	GRPCDialOption(spiffeid.ID) grpc.DialOption
	GRPCDialOptionUnknownTrustDomain(ns, appID string) grpc.DialOption
	GRPCServerOption() grpc.ServerOption
	GRPCServerOptionNoClientAuth() grpc.ServerOption
}

type Provider interface {
	Start(context.Context) error
	// Used for controller-runtime manager non-leader runnable.
	NeedLeaderElection() bool
	Security(context.Context) (Interface, error)
}

// Options are the options for the security authenticator.
type Options struct {
	// SentryAddress is the network address of the sentry server.
	SentryAddress string

	// ControlPlaneTrustDomain is the trust domain of the control plane
	// components.
	ControlPlaneTrustDomain string

	// ControlPlaneNamespace is the dapr namespace of the control plane
	// components.
	ControlPlaneNamespace string

	// TrustAnchors is the X.509 PEM encoded CA certificates for this Dapr
	// installation. Cannot be used with TrustAnchorsFile. TrustAnchorsFile is
	// preferred so changes to the file are automatically picked up.
	TrustAnchors []byte

	// TrustAnchorsFile is the path to the X.509 PEM encoded CA certificates for
	// this Dapr installation. Prefer this over TrustAnchors so changes to the
	// file are automatically picked up. Cannot be used with TrustAnchors.
	TrustAnchorsFile string

	// AppID is the application ID of this workload.
	AppID string

	// MTLS is true if mTLS is enabled.
	MTLSEnabled bool

	// OverrideCertRequestSource is used to override where certificates are requested
	// from. Default to an implementation requesting from Sentry.
	OverrideCertRequestSource RequestFn
}

type provider struct {
	sec *security

	running          atomic.Bool
	readyCh          chan struct{}
	clock            clock.Clock
	trustAnchorsFile string
}

// security implements the Security interface.
type security struct {
	source *x509source
	mtls   bool

	controlPlaneNamespace string
}

func New(opts Options) (Provider, error) {
	if len(opts.ControlPlaneTrustDomain) == 0 {
		return nil, errors.New("control plane trust domain is required")
	}

	var source *x509source

	if !opts.MTLSEnabled {
		log.Warn("mTLS is disabled. Skipping certificate request and tls validation")
	} else {

		if len(opts.TrustAnchors) > 0 && len(opts.TrustAnchorsFile) > 0 {
			return nil, errors.New("trust anchors cannot be specified in both TrustAnchors and TrustAnchorsFile")
		}

		if len(opts.TrustAnchors) == 0 && len(opts.TrustAnchorsFile) == 0 {
			return nil, errors.New("trust anchors are required")
		}

		var err error
		source, err = newX509Source(opts)
		if err != nil {
			return nil, err
		}

		log.Infof("security is initialized successfully")
	}

	return &provider{
		readyCh:          make(chan struct{}),
		clock:            clock.RealClock{},
		trustAnchorsFile: opts.TrustAnchorsFile,
		sec: &security{
			source:                source,
			mtls:                  opts.MTLSEnabled,
			controlPlaneNamespace: opts.ControlPlaneNamespace,
		},
	}, nil
}

// Start is a blocking function which starts the security provider, handling
// rotation of credentials.
func (p *provider) Start(ctx context.Context) error {
	if !p.running.CompareAndSwap(false, true) {
		return errors.New("security provider already started")
	}

	if !p.sec.mtls {
		close(p.readyCh)
		<-ctx.Done()
		return nil
	}

	if p.sec.source.requestFn == nil {
		p.sec.source.requestFn = p.sec.source.requestFromSentry
	}

	initialCert, err := p.sec.source.renewIdentityCertificate(ctx)
	if err != nil {
		return fmt.Errorf("failed to retrieve the initial identity certificate: %w", err)
	}

	mngr := concurrency.NewRunnerManager(func(ctx context.Context) error {
		startRotation(ctx, p.clock, p.sec.source.renewIdentityCertificate, initialCert)
		return nil
	})

	if len(p.trustAnchorsFile) > 0 {
		caEvent := make(chan struct{})

		err = mngr.Add(
			func(ctx context.Context) error {
				log.Infof("watching trust anchors file %q for changes", p.trustAnchorsFile)
				return fswatcher.Watch(ctx, p.trustAnchorsFile, caEvent)
			},
			func(ctx context.Context) error {
				for {
					select {
					case <-ctx.Done():
						return nil
					case <-caEvent:
						log.Info("trust anchors file changed, reloading trust anchors")

						rootPEMs, err := os.ReadFile(p.trustAnchorsFile)
						if err != nil {
							log.Errorf("failed to read trust anchors file %q: %s", p.trustAnchorsFile, err)
							continue
						}

						trustAnchorCerts, err := secpem.DecodePEMCertificates(rootPEMs)
						if err != nil {
							log.Errorf("failed to decode trust anchors: %s", err)
							continue
						}

						p.sec.source.trustAnchors.SetX509Authorities(trustAnchorCerts)
					}
				}
			},
		)
		if err != nil {
			return err
		}
	}

	diagnostics.DefaultMonitoring.MTLSInitCompleted()
	close(p.readyCh)

	return mngr.Run(ctx)
}

func (p *provider) NeedLeaderElection() bool { return false }

// Security returns the security provider. Blocks until the provider is ready.
func (p *provider) Security(ctx context.Context) (Interface, error) {
	select {
	case <-p.readyCh:
		return p.sec, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// TLSServerConfigMTLS returns a TLS server config which instruments using the
// current signed server certificate. Authorizes that clients present their
// SPIFFE ID and that it is in the given trust domain.
// If mTLS is disabled, the given trust domain is ignored (however clients are
// still authorized against the trust anchors).
func (s *security) TLSServerConfigMTLS(td spiffeid.TrustDomain) (*tls.Config, error) {
	if !s.mtls {
		return tlsconfig.MTLSServerConfig(s.source, s.source, tlsconfig.AuthorizeAny()), nil
	}

	// Only authorize clients of the given Trust Domain.
	return tlsconfig.MTLSServerConfig(s.source, s.source, tlsconfig.AuthorizeMemberOf(td)), nil
}

// TLSServerConfigBasicTLS returns a TLS server config which instruments using
// the current signed server certificate. Authorizes client certificate chains
// against the trust anchors.
func (s *security) TLSServerConfigBasicTLS() *tls.Config {
	return tlsconfig.MTLSServerConfig(s.source, s.source, tlsconfig.AuthorizeAny())
}

// TLSServerConfigBasicTLSOption returns a TLS server config option which
// instruments using the current signed server certificate. Designed to be used
// with controller-runtime webhook server.
func (s *security) TLSServerConfigBasicTLSOption(c *tls.Config) {
	if c == nil {
		c = new(tls.Config)
	}
	*c = *s.TLSServerConfigBasicTLS()
}

// GRPCDialOption returns a gRPC dial option which instruments client
// authentication using the current signed client certificate.
func (s *security) GRPCDialOption(appID spiffeid.ID) grpc.DialOption {
	if !s.mtls {
		return grpc.WithTransportCredentials(insecure.NewCredentials())
	}
	return grpc.WithTransportCredentials(
		grpccredentials.MTLSClientCredentials(s.source, s.source, tlsconfig.AuthorizeID(appID)),
	)
}

// GRPCDialOptionUnknownTrustDomain returns a gRPC dial option which
// instruments client authentication using the current signed client
// certificate. Doesn't verify the servers trust domain, but does authorize the
// SPIFFE ID path.
// Used for clients which don't know the servers trust domain.
func (s *security) GRPCDialOptionUnknownTrustDomain(ns, appID string) grpc.DialOption {
	if !s.mtls {
		return grpc.WithTransportCredentials(insecure.NewCredentials())
	}

	expID := fmt.Sprintf("/ns/%s/%s", ns, appID)
	matcher := func(actual spiffeid.ID) error {
		if actual.Path() != expID {
			return fmt.Errorf("unexpected SPIFFE ID: %q", actual)
		}
		return nil
	}

	return grpc.WithTransportCredentials(
		grpccredentials.MTLSClientCredentials(s.source, s.source, tlsconfig.AdaptMatcher(matcher)),
	)
}

// GRPCServerOption returns a gRPC server option which instruments
// authentication of clients using the current trust anchors.
func (s *security) GRPCServerOption() grpc.ServerOption {
	if !s.mtls {
		return grpc.Creds(insecure.NewCredentials())
	}

	return grpc.Creds(
		// TODO: It would be better if we could give a subset of trust domains in
		// which this server authorizes.
		grpccredentials.MTLSServerCredentials(s.source, s.source, tlsconfig.AuthorizeAny()),
	)
}

// GRPCServerOptionNoClientAuth returns a gRPC server option which instruments
// authentication of clients using the current trust anchors. Doesn't require
// clients to present a certificate.
func (s *security) GRPCServerOptionNoClientAuth() grpc.ServerOption {
	return grpc.Creds(
		grpccredentials.TLSServerCredentials(s.source),
	)
}

// ControlPlaneTrustDomain returns the trust domain of the sentry server.
func (s *security) ControlPlaneTrustDomain() spiffeid.TrustDomain {
	return s.source.sentryID.TrustDomain()
}

// ControlPlaneNamespace returns the namespace of the sentry server.
func (s *security) ControlPlaneNamespace() string {
	return s.controlPlaneNamespace
}

// CurrentTrustAnchors returns the current trust anchors for this Dapr
// installation.
func (s *security) CurrentTrustAnchors() ([]byte, error) {
	return s.source.trustAnchors.Marshal()
}

// CurrentNamespace returns the namespace of this workload.
func CurrentNamespace() string {
	namespace, ok := os.LookupEnv("NAMESPACE")
	if !ok {
		return "default"
	}
	return namespace
}

// SentryID returns the SPIFFE ID of the sentry server.
func SentryID(sentryTrustDomain, sentryNamespace string) (spiffeid.ID, error) {
	td, err := spiffeid.TrustDomainFromString(sentryTrustDomain)
	if err != nil {
		return spiffeid.ID{}, fmt.Errorf("invalid trust domain: %w", err)
	}

	sentryID, err := spiffeid.FromPathf(td, "/ns/%s/dapr-sentry", sentryNamespace)
	if err != nil {
		return spiffeid.ID{}, fmt.Errorf("failed to parse sentry SPIFFE ID: %w", err)
	}

	return sentryID, nil
}
