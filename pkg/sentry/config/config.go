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

package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/dapr/kit/logger"
	metaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	scheme "github.com/dapr/dapr/pkg/client/clientset/versioned"
	daprGlobalConfig "github.com/dapr/dapr/pkg/config"
	"github.com/dapr/dapr/utils"
)

const (
	kubernetesServiceHostEnvVar = "KUBERNETES_SERVICE_HOST"
	kubernetesConfig            = "kubernetes"
	selfHostedConfig            = "selfhosted"
	defaultPort                 = 50001
	defaultWorkloadCertTTL      = time.Hour * 24
	defaultAllowedClockSkew     = time.Minute * 15

	// defaultDaprSystemConfigName is the default resource object name for Dapr System Config.
	defaultDaprSystemConfigName = "daprsystem"

	// Default RootCertFilename is the filename that holds the root certificate.
	DefaultRootCertFilename = "ca.crt"

	// DefaultIssuerCertFilename is the filename that holds the issuer certificate.
	DefaultIssuerCertFilename = "issuer.crt"

	// DefaultIssuerKeyFilename is the filename that holds the issuer key.
	DefaultIssuerKeyFilename = "issuer.key"
)

var log = logger.NewLogger("dapr.sentry.config")

// Config holds the configuration for the Certificate Authority.
type Config struct {
	Port             int
	TrustDomain      string
	CAStore          string
	WorkloadCertTTL  time.Duration
	AllowedClockSkew time.Duration
	RootCertPath     string
	IssuerCertPath   string
	IssuerKeyPath    string
	Features         []daprGlobalConfig.FeatureSpec
	TokenAudience    *string
}

func (c Config) GetTokenAudiences() (audiences []string) {
	if c.TokenAudience != nil && *c.TokenAudience != "" {
		audiences = strings.Split(*c.TokenAudience, ",")
	}
	return
}

var configGetters = map[string]func(string) (Config, error){
	selfHostedConfig: getSelfhostedConfig,
	kubernetesConfig: getKubernetesConfig,
}

// FromConfigName returns a Sentry configuration based on a configuration spec.
// A default configuration is loaded in case of an error.
func FromConfigName(configName string) (Config, error) {
	var confGetterFn func(string) (Config, error)

	if IsKubernetesHosted() {
		confGetterFn = configGetters[kubernetesConfig]
	} else {
		confGetterFn = configGetters[selfHostedConfig]
	}

	conf, err := confGetterFn(configName)
	if err != nil {
		err = fmt.Errorf("loading default config. couldn't find config name %q: %w", configName, err)
		conf = getDefaultConfig()
	}

	return conf, err
}

func IsKubernetesHosted() bool {
	return os.Getenv(kubernetesServiceHostEnvVar) != ""
}

func getDefaultConfig() Config {
	return Config{
		Port:             defaultPort,
		WorkloadCertTTL:  defaultWorkloadCertTTL,
		AllowedClockSkew: defaultAllowedClockSkew,
	}
}

func getKubernetesConfig(configName string) (Config, error) {
	defaultConfig := getDefaultConfig()

	kubeConf := utils.GetConfig()
	daprClient, err := scheme.NewForConfig(kubeConf)
	if err != nil {
		return defaultConfig, err
	}

	list, err := daprClient.ConfigurationV1alpha1().Configurations(metaV1.NamespaceAll).List(metaV1.ListOptions{})
	if err != nil {
		return defaultConfig, err
	}

	if configName == "" {
		configName = defaultDaprSystemConfigName
	}

	for _, i := range list.Items {
		if i.GetName() == configName {
			spec, _ := json.Marshal(i.Spec)

			var configSpec daprGlobalConfig.ConfigurationSpec
			json.Unmarshal(spec, &configSpec)

			conf := daprGlobalConfig.Configuration{
				Spec: configSpec,
			}
			return parseConfiguration(defaultConfig, &conf)
		}
	}
	return defaultConfig, errors.New("config CRD not found")
}

func getSelfhostedConfig(configName string) (Config, error) {
	defaultConfig := getDefaultConfig()
	daprConfig, _, err := daprGlobalConfig.LoadStandaloneConfiguration(configName)
	if err != nil {
		return defaultConfig, err
	}

	if daprConfig != nil {
		return parseConfiguration(defaultConfig, daprConfig)
	}
	return defaultConfig, nil
}

func parseConfiguration(conf Config, daprConfig *daprGlobalConfig.Configuration) (Config, error) {
	if daprConfig.Spec.MTLSSpec.WorkloadCertTTL != "" {
		d, err := time.ParseDuration(daprConfig.Spec.MTLSSpec.WorkloadCertTTL)
		if err != nil {
			return conf, fmt.Errorf("error parsing WorkloadCertTTL duration: %w", err)
		}

		conf.WorkloadCertTTL = d
	}

	if daprConfig.Spec.MTLSSpec.AllowedClockSkew != "" {
		d, err := time.ParseDuration(daprConfig.Spec.MTLSSpec.AllowedClockSkew)
		if err != nil {
			return conf, fmt.Errorf("error parsing AllowedClockSkew duration: %w", err)
		}

		conf.AllowedClockSkew = d
	}

	conf.Features = daprConfig.Spec.Features

	return conf, nil
}
