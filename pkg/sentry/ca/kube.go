package ca

import (
	"context"
	"path/filepath"

	"github.com/dapr/dapr/pkg/security/pem"
	"github.com/dapr/dapr/pkg/sentry/config"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// kube is a store that uses Kubernetes as the secret store.
type kube struct {
	config    config.Config
	namespace string
	client    kubernetes.Interface
}

func (k *kube) get(ctx context.Context) (caBundle, bool, error) {
	s, err := k.client.CoreV1().Secrets(k.namespace).Get(ctx, TrustBundleK8sName, metav1.GetOptions{})
	if err != nil {
		return caBundle{}, false, err
	}

	trustAnchors, ok := s.Data[filepath.Base(k.config.RootCertPath)]
	if !ok {
		return caBundle{}, false, nil
	}

	issChainPEM, ok := s.Data[filepath.Base(k.config.IssuerCertPath)]
	if !ok {
		return caBundle{}, false, nil
	}

	issChain, err := pem.DecodePEMCertificates(issChainPEM)
	if err != nil {
		return caBundle{}, false, err
	}

	issKeyPem, ok := s.Data[filepath.Base(k.config.IssuerKeyPath)]
	if !ok {
		return caBundle{}, false, nil
	}

	issKey, err := pem.DecodePEMPrivateKey(issKeyPem)
	if err != nil {
		return caBundle{}, false, err
	}

	// Ensure ConfigMap is up to date also.
	cm, err := k.client.CoreV1().ConfigMaps(k.namespace).Get(ctx, TrustBundleK8sName, metav1.GetOptions{})
	if err != nil {
		return caBundle{}, false, err
	}
	if cm.Data[filepath.Base(k.config.RootCertPath)] != string(trustAnchors) {
		return caBundle{}, false, nil
	}

	return caBundle{
		trustAnchors: trustAnchors,
		issChainPEM:  issChainPEM,
		issChain:     issChain,
		issKey:       issKey,
		issKeyPEM:    issKeyPem,
	}, true, nil
}

func (k *kube) store(ctx context.Context, bundle caBundle) error {
	s, err := k.client.CoreV1().Secrets(k.namespace).Get(ctx, TrustBundleK8sName, metav1.GetOptions{})
	if err != nil {
		return err
	}

	s.Data = map[string][]byte{
		filepath.Base(k.config.RootCertPath):   bundle.trustAnchors,
		filepath.Base(k.config.IssuerCertPath): bundle.issChainPEM,
		filepath.Base(k.config.IssuerKeyPath):  bundle.issKeyPEM,
	}

	_, err = k.client.CoreV1().Secrets(k.namespace).Update(ctx, s, metav1.UpdateOptions{})
	if err != nil {
		return err
	}

	cm, err := k.client.CoreV1().ConfigMaps(k.namespace).Get(ctx, TrustBundleK8sName, metav1.GetOptions{})
	if err != nil {
		return err
	}

	cm.Data = map[string]string{
		filepath.Base(k.config.RootCertPath): string(bundle.trustAnchors),
	}

	_, err = k.client.CoreV1().ConfigMaps(k.namespace).Update(ctx, cm, metav1.UpdateOptions{})
	if err != nil {
		return err
	}

	return nil
}
