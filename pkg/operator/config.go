package operator

import (
	"context"
	"fmt"
	"os"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/dapr/dapr/pkg/apis/configuration/v1alpha1"
)

// Config returns an operator config options.
type Config struct {
	MTLSEnabled             bool
	ControlPlaneTrustDomain string
	SentryAddress           string
}

// GetNamespace returns the namespace for Dapr.
func GetNamespace() string {
	return os.Getenv("NAMESPACE")
}

// LoadConfiguration loads the Kubernetes configuration and returns an Operator Config.
func LoadConfiguration(name string, restConfig *rest.Config) (*Config, error) {
	client, err := client.New(restConfig, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("could not get Kubernetes API client: %v", err)
	}

	var conf v1alpha1.Configuration
	key := types.NamespacedName{
		Namespace: GetNamespace(),
		Name:      name,
	}
	if err := client.Get(context.Background(), key, &conf); err != nil {
		return nil, err
	}
	return &Config{
		MTLSEnabled:             conf.Spec.MTLSSpec.Enabled,
		ControlPlaneTrustDomain: conf.Spec.MTLSSpec.ControlPlaneTrustDomain,
		SentryAddress:           conf.Spec.MTLSSpec.SentryAddress,
	}, nil
}
