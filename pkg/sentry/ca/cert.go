package ca

import (
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"net/url"
	"time"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
)

const (
	// caTTL is the CA certificate TTL.
	caTTL = 10 * 365 * 24 * time.Hour
)

// serialNumber returns the serial number of the certificate.
func serialNumber() (*big.Int, error) {
	serialNumLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNum, err := rand.Int(rand.Reader, serialNumLimit)
	if err != nil {
		return nil, fmt.Errorf("error generating serial number: %w", err)
	}
	return serialNum, nil
}

// generateBaseCert returns a base non-CA cert that can be made a workload or CA cert
// By adding subjects, key usage and additional proerties.
func generateBaseCert(ttl, skew time.Duration) (*x509.Certificate, error) {
	serNum, err := serialNumber()
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	// Allow for clock skew with the NotBefore validity bound.
	notBefore := now.Add(-1 * skew)
	notAfter := now.Add(ttl)

	return &x509.Certificate{
		SerialNumber: serNum,
		NotBefore:    notBefore,
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
	}, nil
}

// generateRootCert returns a CA root x509 Certificate.
func generateRootCert(skew time.Duration) (*x509.Certificate, error) {
	cert, err := generateBaseCert(caTTL, skew)
	if err != nil {
		return nil, err
	}

	cert.KeyUsage |= x509.KeyUsageCertSign
	cert.Subject = pkix.Name{CommonName: "cluster.local"}
	cert.IsCA = true
	cert.BasicConstraintsValid = true
	cert.SignatureAlgorithm = x509.ECDSAWithSHA256
	return cert, nil
}

// generateIssuerCert returns a CA issuing x509 Certificate.
func generateIssuerCert(skew time.Duration) (*x509.Certificate, error) {
	cert, err := generateBaseCert(caTTL, skew)
	if err != nil {
		return nil, err
	}

	cert.KeyUsage |= x509.KeyUsageCertSign | x509.KeyUsageCRLSign
	cert.Subject = pkix.Name{CommonName: "cluster.local", Organization: []string{"dapr.io/sentry"}}
	cert.IsCA = true
	cert.BasicConstraintsValid = true
	cert.SignatureAlgorithm = x509.ECDSAWithSHA256
	return cert, nil
}

// generateWorkloadCert returns a CA issuing x509 Certificate.
func generateWorkloadCert(sig x509.SignatureAlgorithm, ttl, skew time.Duration, id spiffeid.ID) (*x509.Certificate, error) {
	cert, err := generateBaseCert(ttl, skew)
	if err != nil {
		return nil, err
	}

	cert.ExtKeyUsage = append(cert.ExtKeyUsage, x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth)
	cert.SignatureAlgorithm = sig
	cert.URIs = []*url.URL{id.URL()}

	return cert, nil
}
