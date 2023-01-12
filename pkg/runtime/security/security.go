package security

import (
	"context"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"

	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/sentry/certs"
	"github.com/dapr/kit/logger"
)

const (
	ecPKType = "EC PRIVATE KEY"
)

var log = logger.NewLogger("dapr.runtime.security")

// NewAuthenticator creates a new authenticator.
func NewAuthenticator(ctx context.Context, opts Options) (Authenticator, error) {
	return newAuthenticator(ctx, opts, generateCSRAndPrivateKey)
}

func generateCSRAndPrivateKey(id string) ([]byte, []byte, error) {
	if id == "" {
		return nil, nil, errors.New("id must not be empty")
	}

	key, err := certs.GenerateECPrivateKey()
	if err != nil {
		diag.DefaultMonitoring.MTLSInitFailed("prikeygen")
		return nil, nil, fmt.Errorf("failed to generate private key: %w", err)
	}

	csr := x509.CertificateRequest{
		Subject:  pkix.Name{CommonName: id},
		DNSNames: []string{id},
	}
	csrb, err := x509.CreateCertificateRequest(rand.Reader, &csr, key)
	if err != nil {
		diag.DefaultMonitoring.MTLSInitFailed("csr")
		return nil, nil, fmt.Errorf("failed to create sidecar csr: %w", err)
	}

	encodedKey, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		diag.DefaultMonitoring.MTLSInitFailed("prikeyenc")
		return nil, nil, err
	}
	keyPem := pem.EncodeToMemory(&pem.Block{Type: ecPKType, Bytes: encodedKey})
	return csrb, keyPem, nil
}
