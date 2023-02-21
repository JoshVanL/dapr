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

package fake

import (
	"crypto/tls"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"google.golang.org/grpc"
)

type Fake struct {
	controlPlaneTrustDomainFn func() spiffeid.TrustDomain
	controlPlaneNamespaceFn   func() string
	trustAnchorsFn            func() []byte
	appTrustDomainFn          func() string

	tlsServerConfigMTLSFn           func(spiffeid.TrustDomain) (*tls.Config, error)
	tlsServerConfigBasicTLSFn       func() *tls.Config
	tlsServerConfigBasicTLSOptionFn func(*tls.Config)

	grpcDialOptionFn                   func(spiffeid.ID) grpc.DialOption
	grpcDialOptionUnknownTrustDomainFn func(ns, appID string) grpc.DialOption
	grpcServerOptionFn                 func() grpc.ServerOption
	grpcServerOptionNoClientAuthFn     func() grpc.ServerOption
}

func New() *Fake {
	return &Fake{
		controlPlaneTrustDomainFn: func() spiffeid.TrustDomain {
			return spiffeid.TrustDomain{}
		},
		controlPlaneNamespaceFn: func() string {
			return ""
		},
		trustAnchorsFn: func() []byte {
			return nil
		},
		appTrustDomainFn: func() string {
			return ""
		},
		tlsServerConfigMTLSFn: func(spiffeid.TrustDomain) (*tls.Config, error) {
			return &tls.Config{}, nil
		},
		tlsServerConfigBasicTLSFn: func() *tls.Config {
			return &tls.Config{}
		},
		tlsServerConfigBasicTLSOptionFn: func(*tls.Config) {},
		grpcDialOptionFn: func(spiffeid.ID) grpc.DialOption {
			return grpc.WithInsecure()
		},
		grpcDialOptionUnknownTrustDomainFn: func(ns, appID string) grpc.DialOption {
			return grpc.WithInsecure()
		},
		grpcServerOptionFn: func() grpc.ServerOption {
			return grpc.Creds(nil)
		},
		grpcServerOptionNoClientAuthFn: func() grpc.ServerOption {
			return grpc.Creds(nil)
		},
	}
}

func (f *Fake) WithControlPlaneTrustDomainFn(fn func() spiffeid.TrustDomain) *Fake {
	f.controlPlaneTrustDomainFn = fn
	return f
}

func (f *Fake) WithControlPlaneNamespaceFn(fn func() string) *Fake {
	f.controlPlaneNamespaceFn = fn
	return f
}

func (f *Fake) WithTrustAnchorsFn(fn func() []byte) *Fake {
	f.trustAnchorsFn = fn
	return f
}

func (f *Fake) WithAppTrustDomainFn(fn func() string) *Fake {
	f.appTrustDomainFn = fn
	return f
}

func (f *Fake) WithTLSServerConfigMTLSFn(fn func(spiffeid.TrustDomain) (*tls.Config, error)) *Fake {
	f.tlsServerConfigMTLSFn = fn
	return f
}

func (f *Fake) WithTLSServerConfigBasicTLSFn(fn func() *tls.Config) *Fake {
	f.tlsServerConfigBasicTLSFn = fn
	return f
}

func (f *Fake) WithTLSServerConfigBasicTLSOptionFn(fn func(*tls.Config)) *Fake {
	f.tlsServerConfigBasicTLSOptionFn = fn
	return f
}

func (f *Fake) WithGRPCDialOptionFn(fn func(spiffeid.ID) grpc.DialOption) *Fake {
	f.grpcDialOptionFn = fn
	return f
}

func (f *Fake) WithGRPCDialOptionUnknownTrustDomainFn(fn func(ns, appID string) grpc.DialOption) *Fake {
	f.grpcDialOptionUnknownTrustDomainFn = fn
	return f
}

func (f *Fake) WithGRPCServerOptionFn(fn func() grpc.ServerOption) *Fake {
	f.grpcServerOptionFn = fn
	return f
}

func (f *Fake) WithGRPCServerOptionNoClientAuthFn(fn func() grpc.ServerOption) *Fake {
	f.grpcServerOptionNoClientAuthFn = fn
	return f
}

func (f *Fake) ControlPlaneTrustDomain() spiffeid.TrustDomain {
	return f.controlPlaneTrustDomainFn()
}

func (f *Fake) ControlPlaneNamespace() string {
	return f.controlPlaneNamespaceFn()
}

func (f *Fake) TrustAnchors() []byte {
	return f.trustAnchorsFn()
}

func (f *Fake) AppTrustDomain() string {
	return f.appTrustDomainFn()
}

func (f *Fake) TLSServerConfigMTLS(td spiffeid.TrustDomain) (*tls.Config, error) {
	return f.tlsServerConfigMTLSFn(td)
}

func (f *Fake) TLSServerConfigBasicTLS() *tls.Config {
	return f.tlsServerConfigBasicTLSFn()
}

func (f *Fake) TLSServerConfigBasicTLSOption(cfg *tls.Config) {
	f.tlsServerConfigBasicTLSOptionFn(cfg)
}

func (f *Fake) GRPCDialOption(id spiffeid.ID) grpc.DialOption {
	return f.grpcDialOptionFn(id)
}

func (f *Fake) GRPCDialOptionUnknownTrustDomain(ns, appID string) grpc.DialOption {
	return f.grpcDialOptionUnknownTrustDomainFn(ns, appID)
}

func (f *Fake) GRPCServerOption() grpc.ServerOption {
	return f.grpcServerOptionFn()
}

func (f *Fake) GRPCServerOptionNoClientAuth() grpc.ServerOption {
	return f.grpcServerOptionNoClientAuthFn()
}
