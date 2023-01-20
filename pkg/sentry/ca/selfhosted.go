package ca

import (
	"context"
	"os"

	"github.com/dapr/dapr/pkg/security/pem"
	"github.com/dapr/dapr/pkg/sentry/config"
)

// selfSigned is a store that uses the file system as the secret store.
type selfhosted struct {
	config config.Config
}

func (s *selfhosted) store(_ context.Context, bundle caBundle) error {
	for _, f := range []struct {
		name string
		data []byte
	}{
		{s.config.RootCertPath, bundle.trustAnchors},
		{s.config.IssuerCertPath, bundle.issChainPEM},
		{s.config.IssuerKeyPath, bundle.issKeyPEM},
	} {
		if err := os.WriteFile(f.name, f.data, 0600); err != nil {
			return err
		}
	}

	return nil
}

func (s *selfhosted) get(_ context.Context) (caBundle, bool, error) {
	trustAnchors, err := os.ReadFile(s.config.RootCertPath)
	if os.IsNotExist(err) {
		return caBundle{}, false, nil
	}
	if err != nil {
		return caBundle{}, false, err
	}

	issChainPEM, err := os.ReadFile(s.config.IssuerCertPath)
	if os.IsNotExist(err) {
		return caBundle{}, false, nil
	}
	if err != nil {
		return caBundle{}, false, err
	}

	issChain, err := pem.DecodePEMCertificates(issChainPEM)
	if err != nil {
		return caBundle{}, false, err
	}

	issKeyPem, err := os.ReadFile(s.config.IssuerKeyPath)
	if os.IsNotExist(err) {
		return caBundle{}, false, nil
	}
	if err != nil {
		return caBundle{}, false, err
	}

	issKey, err := pem.DecodePEMPrivateKey(issKeyPem)
	if err != nil {
		return caBundle{}, false, err
	}

	return caBundle{
		trustAnchors: trustAnchors,
		issChainPEM:  issChainPEM,
		issChain:     issChain,
		issKeyPEM:    issKeyPem,
		issKey:       issKey,
	}, true, nil
}
