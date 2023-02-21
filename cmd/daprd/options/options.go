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

package options

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/dapr/dapr/pkg/apphealth"
	"github.com/dapr/dapr/pkg/buildinfo"
	env "github.com/dapr/dapr/pkg/config/env"
	"github.com/dapr/dapr/pkg/cors"
	"github.com/dapr/dapr/pkg/metrics"
	"github.com/dapr/dapr/pkg/modes"
	"github.com/dapr/dapr/pkg/runtime"
	"github.com/dapr/dapr/pkg/runtime/wait"
	"github.com/dapr/dapr/pkg/security/consts"
	"github.com/dapr/dapr/pkg/validation"
	"github.com/dapr/dapr/utils"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.options")

type Options struct {
	Mode                         string
	APIListenAddresses           []string
	HTTPPort                     int
	PublicPort                   *int
	APIGRPCPort                  int
	InternalGRPCPort             int
	AppPort                      int
	ProfilePort                  int
	AppProtocol                  string
	ComponentsPath               string
	ResourcesPath                []string
	Config                       string
	AppID                        string
	ControlPlaneAddress          string
	SentryHost                   string
	PlacementAddresses           []string
	AllowedOrigins               string
	EnableProfiling              bool
	AppMaxConcurrency            int
	EnableMTLS                   bool
	AppSSL                       bool
	HTTPMaxRequestBodySize       int
	UnixDomainSocket             string
	HTTPReadBufferSize           int
	GracefulShutdownDuration     time.Duration
	EnableAPILogging             bool
	DisableBuiltinK8sSecretStore bool
	EnableAppHealthCheck         bool
	AppHealthCheckPath           string
	AppHealthProbeInterval       time.Duration
	AppHealthProbeTimeout        time.Duration
	AppHealthThreshold           int32
	ControlPlaneNamespace        string
	ControlPlaneTrustDomain      string
	TrustAnchors                 []byte
	Namespace                    string
}

var opts Options

func Parse() (Options, error) {
	if flag.Parsed() {
		return opts, nil
	}

	var (
		daprHTTPPort                string
		daprPublicPort              string
		daprAPIGRPCPort             string
		daprInternalGRPCPort        string
		appPort                     string
		profilePort                 string
		resourcesPath               stringSliceFlag
		runtimeVersion              bool
		buildInfo                   bool
		waitCommand                 bool
		daprAPIListAddresses        string
		appHealthProbeInterval      int
		appHealthProbeTimeout       int
		daprHTTPMaxRequestSize      int
		daprHTTPReadBufferSize      int
		daprGracefulShutdownSeconds int
		placementServiceHostAddr    string
		appHealthThreshold          int
	)

	flag.StringVar(&opts.Mode, "mode", string(modes.StandaloneMode), "Runtime mode for Dapr")
	flag.StringVar(&daprHTTPPort, "dapr-http-port", strconv.Itoa(runtime.DefaultDaprHTTPPort), "HTTP port for Dapr API to listen on")
	flag.StringVar(&daprAPIListAddresses, "dapr-listen-addresses", runtime.DefaultAPIListenAddress, "One or more addresses for the Dapr API to listen on, CSV limited")
	flag.StringVar(&daprPublicPort, "dapr-public-port", "", "Public port for Dapr Health and Metadata to listen on")
	flag.StringVar(&daprAPIGRPCPort, "dapr-grpc-port", strconv.Itoa(runtime.DefaultDaprAPIGRPCPort), "gRPC port for the Dapr API to listen on")
	flag.StringVar(&daprInternalGRPCPort, "dapr-internal-grpc-port", "", "gRPC port for the Dapr Internal API to listen on")
	flag.StringVar(&appPort, "app-port", "", "The port the application is listening on")
	flag.StringVar(&profilePort, "profile-port", strconv.Itoa(runtime.DefaultProfilePort), "The port for the profile server")
	flag.StringVar(&opts.AppProtocol, "app-protocol", string(runtime.HTTPProtocol), "Protocol for the application: grpc or http")
	flag.StringVar(&opts.ComponentsPath, "components-path", "", "Alias for --resources-path [Deprecated, use --resources-path]")
	flag.Var(&resourcesPath, "resources-path", "Path for resources directory. If not specified, no resources will be loaded. Can be passed multiple times")
	flag.StringVar(&opts.Config, "config", "", "Path to config file, or name of a configuration object")
	flag.StringVar(&opts.AppID, "app-id", "", "A unique ID for Dapr. Used for Service Discovery and state")
	flag.StringVar(&opts.ControlPlaneAddress, "control-plane-address", "", "Address for a Dapr control plane")
	flag.StringVar(&opts.SentryHost, "sentry-address", "", "Address for the Sentry CA service")
	flag.StringVar(&placementServiceHostAddr, "placement-host-address", "", "Addresses for Dapr Actor Placement servers")
	flag.StringVar(&opts.AllowedOrigins, "allowed-origins", cors.DefaultAllowedOrigins, "Allowed HTTP origins")
	flag.BoolVar(&opts.EnableProfiling, "enable-profiling", false, "Enable profiling")
	flag.BoolVar(&runtimeVersion, "version", false, "Prints the runtime version")
	flag.BoolVar(&buildInfo, "build-info", false, "Prints the build info")
	flag.BoolVar(&waitCommand, "wait", false, "wait for Dapr outbound ready")
	flag.IntVar(&opts.AppMaxConcurrency, "app-max-concurrency", -1, "Controls the concurrency level when forwarding requests to user code; set to -1 for no limits")
	flag.BoolVar(&opts.EnableMTLS, "enable-mtls", false, "Enables automatic mTLS for daprd to daprd communication channels")
	flag.BoolVar(&opts.AppSSL, "app-ssl", false, "Sets the URI scheme of the app to https and attempts an SSL connection")
	flag.IntVar(&daprHTTPMaxRequestSize, "dapr-http-max-request-size", runtime.DefaultMaxRequestBodySize, "Increasing max size of request body in MB to handle uploading of big files")
	flag.StringVar(&opts.UnixDomainSocket, "unix-domain-socket", "", "Path to a unix domain socket dir mount. If specified, Dapr API servers will use Unix Domain Sockets")
	flag.IntVar(&daprHTTPReadBufferSize, "dapr-http-read-buffer-size", runtime.DefaultReadBufferSize, "Increasing max size of read buffer in KB to handle sending multi-KB headers")
	flag.IntVar(&daprGracefulShutdownSeconds, "dapr-graceful-shutdown-seconds", int(runtime.DefaultGracefulShutdownDuration/time.Second), "Graceful shutdown time in seconds")
	flag.BoolVar(&opts.EnableAPILogging, "enable-api-logging", false, "Enable API logging for API calls")
	flag.BoolVar(&opts.DisableBuiltinK8sSecretStore, "disable-builtin-k8s-secret-store", false, "Disable the built-in Kubernetes Secret Store")
	flag.BoolVar(&opts.EnableAppHealthCheck, "enable-app-health-check", false, "Enable health checks for the application using the protocol defined with app-protocol")
	flag.StringVar(&opts.AppHealthCheckPath, "app-health-check-path", runtime.DefaultAppHealthCheckPath, "Path used for health checks; HTTP only")
	flag.IntVar(&appHealthProbeInterval, "app-health-probe-interval", int(apphealth.DefaultProbeInterval/time.Second), "Interval to probe for the health of the app in seconds")
	flag.IntVar(&appHealthProbeTimeout, "app-health-probe-timeout", int(apphealth.DefaultProbeTimeout/time.Millisecond), "Timeout for app health probes in milliseconds")
	flag.IntVar(&appHealthThreshold, "app-health-threshold", int(apphealth.DefaultThreshold), "Number of consecutive failures for the app to be considered unhealthy")
	flag.StringVar(&opts.ControlPlaneNamespace, "control-plane-namespace", "dapr-system", "Namespace of the Dapr control plane")
	flag.StringVar(&opts.ControlPlaneTrustDomain, "control-plane-trust-domain", "localhost", "Trust domain of the Dapr control plane")

	loggerOptions := logger.DefaultOptions()
	loggerOptions.AttachCmdFlags(flag.StringVar, flag.BoolVar)

	metricsExporter := metrics.NewExporter(metrics.DefaultMetricNamespace)
	metricsExporter.Options().AttachCmdFlags(flag.StringVar, flag.BoolVar)

	flag.Parse()

	if runtimeVersion {
		fmt.Println(buildinfo.Version())
		os.Exit(0)
	}
	if buildInfo {
		fmt.Printf("Version: %s\nGit Commit: %s\nGit Version: %s\n", buildinfo.Version(), buildinfo.Commit(), buildinfo.GitVersion())
		os.Exit(0)
	}

	if waitCommand {
		wait.UntilDaprOutboundReady(daprHTTPPort)
		os.Exit(0)
	}

	opts.ResourcesPath = resourcesPath
	if len(opts.ResourcesPath) == 0 && len(opts.ComponentsPath) > 0 {
		opts.ResourcesPath = stringSliceFlag{opts.ComponentsPath}
	}

	if opts.Mode == string(modes.StandaloneMode) {
		if err := validation.ValidateSelfHostedAppID(opts.AppID); err != nil {
			return Options{}, err
		}
	}

	// Apply options to all loggers
	loggerOptions.SetAppID(opts.AppID)
	if err := logger.ApplyOptionsToLoggers(&loggerOptions); err != nil {
		return Options{}, err
	}
	log.Infof("log level set to: %s", loggerOptions.OutputLevel)

	// Initialize dapr metrics exporter
	if err := metricsExporter.Init(); err != nil {
		log.Fatal(err)
	}

	var err error
	opts.HTTPPort, err = strconv.Atoi(daprHTTPPort)
	if err != nil {
		return Options{}, fmt.Errorf("error parsing dapr-http-port flag: %w", err)
	}

	opts.APIGRPCPort, err = strconv.Atoi(daprAPIGRPCPort)
	if err != nil {
		return Options{}, fmt.Errorf("error parsing dapr-grpc-port flag: %w", err)
	}

	opts.ProfilePort, err = strconv.Atoi(profilePort)
	if err != nil {
		return Options{}, fmt.Errorf("error parsing profile-port flag: %w", err)
	}

	if daprInternalGRPCPort != "" && daprInternalGRPCPort != "0" {
		opts.InternalGRPCPort, err = strconv.Atoi(daprInternalGRPCPort)
		if err != nil {
			return Options{}, fmt.Errorf("error parsing dapr-internal-grpc-port: %w", err)
		}
	} else {
		// Get a "stable random" port in the range 47300-49,347 if it can be acquired using a deterministic algorithm that returns the same value if the same app is restarted
		// Otherwise, the port will be random.
		opts.InternalGRPCPort, err = utils.GetStablePort(47300, opts.AppID)
		if err != nil {
			return Options{}, fmt.Errorf("failed to get free port for internal grpc server: %w", err)
		}
	}

	if daprPublicPort != "" {
		port, err := strconv.Atoi(daprPublicPort)
		if err != nil {
			return Options{}, fmt.Errorf("error parsing dapr-public-port: %w", err)
		}
		opts.PublicPort = &port
	}

	if appPort != "" {
		opts.AppPort, err = strconv.Atoi(appPort)
		if err != nil {
			return Options{}, fmt.Errorf("error parsing app-port: %w", err)
		}
	}

	if opts.AppPort == opts.HTTPPort {
		return Options{}, fmt.Errorf("the 'dapr-http-port' argument value %d conflicts with 'app-port'", daprHTTPPort)
	}

	if opts.AppPort == opts.APIGRPCPort {
		return Options{}, fmt.Errorf("the 'dapr-grpc-port' argument value %d conflicts with 'app-port'", daprAPIGRPCPort)
	}

	if daprHTTPMaxRequestSize != -1 {
		opts.HTTPMaxRequestBodySize = daprHTTPMaxRequestSize
	} else {
		opts.HTTPMaxRequestBodySize = runtime.DefaultMaxRequestBodySize
	}

	if daprHTTPReadBufferSize != -1 {
		opts.HTTPReadBufferSize = daprHTTPReadBufferSize
	} else {
		opts.HTTPReadBufferSize = runtime.DefaultReadBufferSize
	}

	if daprGracefulShutdownSeconds < 0 {
		opts.GracefulShutdownDuration = runtime.DefaultGracefulShutdownDuration
	} else {
		opts.GracefulShutdownDuration = time.Duration(daprGracefulShutdownSeconds) * time.Second
	}

	if placementServiceHostAddr != "" {
		opts.PlacementAddresses = parsePlacementAddr(placementServiceHostAddr)
	}

	if opts.AppMaxConcurrency == -1 {
		opts.AppMaxConcurrency = 0
	}

	opts.AppProtocol = strings.ToLower(opts.AppProtocol)
	switch opts.AppProtocol {
	case string(runtime.HTTPProtocol), string(runtime.GRPCProtocol):
	case "":
		opts.AppProtocol = string(runtime.HTTPProtocol)
	default:
		return Options{}, fmt.Errorf("invalid value for 'app-protocol': %v", opts.AppProtocol)
	}

	opts.APIListenAddresses = strings.Split(daprAPIListAddresses, ",")
	if len(opts.APIListenAddresses) == 0 {
		opts.APIListenAddresses = []string{runtime.DefaultAPIListenAddress}
	}

	if appHealthProbeInterval <= 0 {
		opts.AppHealthProbeInterval = apphealth.DefaultProbeInterval
	} else {
		opts.AppHealthProbeInterval = time.Duration(appHealthProbeInterval) * time.Second
	}

	if appHealthProbeTimeout <= 0 {
		opts.AppHealthProbeTimeout = apphealth.DefaultProbeTimeout
	} else {
		opts.AppHealthProbeTimeout = time.Duration(appHealthProbeTimeout) * time.Millisecond
	}

	if opts.AppHealthProbeTimeout > opts.AppHealthProbeInterval {
		return Options{}, errors.New("value for 'health-probe-timeout' must be smaller than 'health-probe-interval'")
	}

	// Also check to ensure no overflow with int32
	if appHealthThreshold < 1 || int32(appHealthThreshold+1) < 0 {
		opts.AppHealthThreshold = apphealth.DefaultThreshold
	} else {
		opts.AppHealthThreshold = int32(appHealthThreshold)
	}

	opts.TrustAnchors = []byte(os.Getenv(consts.TrustAnchorsEnvVar))

	// set environment variables
	// TODO - consider adding host address to runtime config and/or caching result in utils package
	host, err := utils.GetHostAddress()
	if err != nil {
		log.Warnf("failed to get host address, env variable %s will not be set", env.HostAddress)
	}

	if err = utils.SetEnvVariables(map[string]string{
		env.AppID:           opts.AppID,
		env.AppPort:         strconv.Itoa(opts.AppPort),
		env.HostAddress:     host,
		env.DaprPort:        strconv.Itoa(opts.InternalGRPCPort),
		env.DaprGRPCPort:    strconv.Itoa(opts.APIGRPCPort),
		env.DaprHTTPPort:    strconv.Itoa(opts.HTTPPort),
		env.DaprMetricsPort: metricsExporter.Options().Port, // TODO - consider adding to runtime config
		env.DaprProfilePort: strconv.Itoa(opts.ProfilePort),
	}); err != nil {
		return Options{}, err
	}

	opts.Namespace = os.Getenv("NAMESPACE")

	return opts, nil
}

// Flag type. Allows passing a flag multiple times to get a slice of strings.
// It implements the flag.Value interface.
type stringSliceFlag []string

// String formats the flag value.
func (f stringSliceFlag) String() string {
	return strings.Join(f, ",")
}

// Set the flag value.
func (f *stringSliceFlag) Set(value string) error {
	if value == "" {
		return errors.New("value is empty")
	}
	*f = append(*f, value)
	return nil
}

func parsePlacementAddr(val string) []string {
	parsed := []string{}
	p := strings.Split(val, ",")
	for _, addr := range p {
		parsed = append(parsed, strings.TrimSpace(addr))
	}
	return parsed
}
