/*
Copyright 2021 The Dapr Authors
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

package operator

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	apiextensionsclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	componentsapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	configurationapi "github.com/dapr/dapr/pkg/apis/configuration/v1alpha1"
	resiliencyapi "github.com/dapr/dapr/pkg/apis/resiliency/v1alpha1"
	subscriptionsapiV1alpha1 "github.com/dapr/dapr/pkg/apis/subscriptions/v1alpha1"
	subscriptionsapiV2alpha1 "github.com/dapr/dapr/pkg/apis/subscriptions/v2alpha1"
	"github.com/dapr/dapr/pkg/health"
	"github.com/dapr/dapr/pkg/operator/api"
	operatorcache "github.com/dapr/dapr/pkg/operator/cache"
	"github.com/dapr/dapr/pkg/operator/handlers"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.operator")

const (
	healthzPort = 8080
)

// Operator is an Dapr Kubernetes Operator for managing components and sidecar lifecycle.
type Operator interface {
	Run(ctx context.Context) error
}

// Options contains the options for `NewOperator`.
type Options struct {
	Config                              string
	LeaderElection                      bool
	WatchdogEnabled                     bool
	WatchdogInterval                    time.Duration
	WatchdogMaxRestartsPerMin           int
	WatchNamespace                      string
	ServiceReconcilerEnabled            bool
	ArgoRolloutServiceReconcilerEnabled bool
	WatchdogCanPatchPodLabels           bool

	ControlPlaneNamespace string
	TrustAnchorsFile      string
}

type operator struct {
	apiServer api.Server

	config *Config

	namespace string

	mgr         ctrl.Manager
	secProvider security.Provider
}

var scheme = runtime.NewScheme()

func init() {
	_ = clientgoscheme.AddToScheme(scheme)

	_ = componentsapi.AddToScheme(scheme)
	_ = configurationapi.AddToScheme(scheme)
	_ = resiliencyapi.AddToScheme(scheme)
	_ = subscriptionsapiV1alpha1.AddToScheme(scheme)
	_ = subscriptionsapiV2alpha1.AddToScheme(scheme)
}

// NewOperator returns a new Dapr Operator.
func NewOperator(ctx context.Context, opts Options) (Operator, error) {
	conf, err := ctrl.GetConfig()
	if err != nil {
		return nil, fmt.Errorf("unable to get controller runtime configuration, err: %s", err)
	}

	config, err := LoadConfiguration(opts.Config, conf)
	if err != nil {
		return nil, fmt.Errorf("unable to load configuration, config: %s, err: %w", opts.Config, err)
	}

	secProvider, err := security.New(security.Options{
		SentryAddress:           config.SentryAddress,
		ControlPlaneTrustDomain: config.ControlPlaneTrustDomain,
		ControlPlaneNamespace:   opts.ControlPlaneNamespace,
		TrustAnchorsFile:        opts.TrustAnchorsFile,
		AppID:                   "dapr-operator",
		MTLSEnabled:             config.MTLSEnabled,
	})
	if err != nil {
		return nil, err
	}
	webhook.Server{}

	watchdogPodSelector := getSideCarInjectedNotExistsSelector()
	mgr, err := ctrl.NewManager(conf, ctrl.Options{
		Scheme:                 scheme,
		Port:                   19443,
		HealthProbeBindAddress: "0",
		MetricsBindAddress:     "0",
		LeaderElection:         opts.LeaderElection,
		LeaderElectionID:       "operator.dapr.io",
		Namespace:              opts.WatchNamespace,
		NewCache:               operatorcache.GetFilteredCache(watchdogPodSelector),
		TLSOpts: []func(*tls.Config){
			func(tlsConfig *tls.Config) {
				sec, err := secProvider.Security(ctx)
				// Error here means that the context has been cancelled before security
				// is ready.
				if err != nil {
					return
				}
				sec.TLSServerConfigBasicTLSOption(tlsConfig)
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("unable to start manager: %w", err)
	}
	mgrClient := mgr.GetClient()

	if opts.WatchdogEnabled {
		if !opts.LeaderElection {
			log.Warn("Leadership election is forcibly enabled when the Dapr Watchdog is enabled")
		}
		wd := &DaprWatchdog{
			client:            mgrClient,
			interval:          opts.WatchdogInterval,
			maxRestartsPerMin: opts.WatchdogMaxRestartsPerMin,
			canPatchPodLabels: opts.WatchdogCanPatchPodLabels,
			podSelector:       watchdogPodSelector,
		}
		if err := mgr.Add(wd); err != nil {
			return nil, fmt.Errorf("unable to add watchdog controller: %w", err)
		}
	} else {
		log.Infof("Dapr Watchdog is not enabled")
	}

	if opts.ServiceReconcilerEnabled {
		daprHandler := handlers.NewDaprHandlerWithOptions(mgr, &handlers.Options{ArgoRolloutServiceReconcilerEnabled: opts.ArgoRolloutServiceReconcilerEnabled})
		if err := daprHandler.Init(ctx); err != nil {
			return nil, fmt.Errorf("unable to initialize handler: %w", err)
		}
	}

	return &operator{
		mgr:         mgr,
		namespace:   opts.ControlPlaneNamespace,
		secProvider: secProvider,
		config:      config,
		apiServer:   api.NewAPIServer(mgrClient),
	}, nil
}

func (o *operator) syncComponent(ctx context.Context) func(obj interface{}) {
	return func(obj interface{}) {
		c, ok := obj.(*componentsapi.Component)
		if ok {
			log.Debugf("Observed component to be synced: %s/%s", c.Namespace, c.Name)
			o.apiServer.OnComponentUpdated(ctx, c)
		}
	}
}

func (o *operator) Run(ctx context.Context) error {
	log.Info("Dapr Operator is starting")
	healthzServer := health.NewServer(log)

	err := o.mgr.Add(nonLeaderRunnable{func(ctx context.Context) error {
		// start healthz server
		if rErr := healthzServer.Run(ctx, healthzPort); rErr != nil {
			return fmt.Errorf("failed to start healthz server: %w", rErr)
		}
		return nil
	}})
	if err != nil {
		return err
	}

	err = o.mgr.Add(nonLeaderRunnable{func(ctx context.Context) error {
		if rErr := o.apiServer.Ready(ctx); rErr != nil {
			return fmt.Errorf("failed to start API server: %w", rErr)
		}
		healthzServer.Ready()
		log.Infof("Dapr Operator started")
		<-ctx.Done()
		return nil
	}})
	if err != nil {
		return err
	}

	if err := o.mgr.Add(o.secProvider); err != nil {
		return fmt.Errorf("unable to add security provider: %w", err)
	}

	err = o.mgr.Add(nonLeaderRunnable{func(ctx context.Context) error {
		/*
			Make sure to set `ENABLE_WEBHOOKS=false` when we run locally.
		*/
		var rErr error
		if !strings.EqualFold(os.Getenv("ENABLE_WEBHOOKS"), "false") {
			rErr = ctrl.NewWebhookManagedBy(o.mgr).
				For(&subscriptionsapiV1alpha1.Subscription{}).
				Complete()
			if rErr != nil {
				return fmt.Errorf("unable to create webhook Subscriptions v1alpha1: %w", rErr)
			}
			rErr = ctrl.NewWebhookManagedBy(o.mgr).
				For(&subscriptionsapiV2alpha1.Subscription{}).
				Complete()
			if rErr != nil {
				return fmt.Errorf("unable to create webhook Subscriptions v2alpha1: %w", rErr)
			}
		}

		rErr = o.patchCRDs(ctx, o.mgr.GetConfig(), "subscriptions.dapr.io")
		if rErr != nil {
			return rErr
		}

		sec, err := o.secProvider.Security(ctx)
		if err != nil {
			return err
		}

		log.Info("Starting api server")
		rErr = o.apiServer.Run(ctx, sec)
		if rErr != nil {
			return fmt.Errorf("failed to start API server: %w", rErr)
		}
		return nil
	}})
	if err != nil {
		return err
	}

	err = o.mgr.Add(nonLeaderRunnable{func(ctx context.Context) error {
		if !o.mgr.GetCache().WaitForCacheSync(ctx) {
			return errors.New("failed to wait for cache sync")
		}

		componentInformer, rErr := o.mgr.GetCache().GetInformer(ctx, &componentsapi.Component{})
		if rErr != nil {
			return fmt.Errorf("unable to get setup components informer: %w", rErr)
		}

		_, rErr = componentInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
			AddFunc: o.syncComponent(ctx),
			UpdateFunc: func(_, newObj interface{}) {
				o.syncComponent(ctx)(newObj)
			},
		})
		if rErr != nil {
			return fmt.Errorf("unable to add components informer event handler: %w", rErr)
		}
		<-ctx.Done()
		return nil
	}})
	if err != nil {
		return err
	}

	err = o.mgr.Start(ctx)
	if err != nil {
		return fmt.Errorf("error running operator: %w", err)
	}

	return nil
}

func (o *operator) patchCRDs(ctx context.Context, conf *rest.Config, crdNames ...string) error {
	client, err := kubernetes.NewForConfig(conf)
	if err != nil {
		return fmt.Errorf("could not get Kubernetes API client: %v", err)
	}

	clientSet, err := apiextensionsclient.NewForConfig(conf)
	if err != nil {
		return fmt.Errorf("could not get API extension client: %v", err)
	}

	crdClient := clientSet.ApiextensionsV1().CustomResourceDefinitions()

	//si, err := client.CoreV1().Secrets(o.namespace).Get(ctx, webhookCAName, v1.GetOptions{})
	//if err != nil {
	//	log.Debugf("Could not get webhook CA: %v", err)
	//	log.Info("The webhook CA secret was not found. Assuming conversion webhook caBundles are managed manually.")
	//	return nil
	//}

	//caBundle, ok := si.Data["caBundle"]
	//if !ok {
	//	return errors.New("webhook CA secret did not contain 'caBundle'")
	//}

	for _, crdName := range crdNames {
		crd, err := crdClient.Get(ctx, crdName, v1.GetOptions{})
		if err != nil {
			return fmt.Errorf("could not get CRD %q: %v", crdName, err)
		}

		if crd == nil ||
			crd.Spec.Conversion == nil ||
			crd.Spec.Conversion.Webhook == nil ||
			crd.Spec.Conversion.Webhook.ClientConfig == nil {
			return fmt.Errorf("crd %q does not have an existing webhook client config. Applying resources of this type will fail", crdName)
		}

		if crd.Spec.Conversion.Webhook.ClientConfig.Service != nil &&
			crd.Spec.Conversion.Webhook.ClientConfig.Service.Namespace == o.namespace &&
			crd.Spec.Conversion.Webhook.ClientConfig.CABundle != nil &&
			bytes.Equal(crd.Spec.Conversion.Webhook.ClientConfig.CABundle, caBundle) {
			log.Infof("Conversion webhook for %q is up to date", crdName)

			continue
		}

		// This code mimics:
		// kubectl patch crd "subscriptions.dapr.io" --type='json' -p [{'op': 'replace', 'path': '/spec/conversion/webhook/clientConfig/service/namespace', 'value':'${namespace}'},{'op': 'add', 'path': '/spec/conversion/webhook/clientConfig/caBundle', 'value':'${caBundle}'}]"
		type patchValue struct {
			Op    string      `json:"op"`
			Path  string      `json:"path"`
			Value interface{} `json:"value"`
		}
		payload := []patchValue{{
			Op:    "replace",
			Path:  "/spec/conversion/webhook/clientConfig/service/namespace",
			Value: o.namespace,
		}, {
			Op:    "replace",
			Path:  "/spec/conversion/webhook/clientConfig/caBundle",
			Value: caBundle,
		}}

		payloadJSON, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("could not marshal webhook spec: %v", err)
		}
		if _, err := crdClient.Patch(ctx, crdName, types.JSONPatchType, payloadJSON, v1.PatchOptions{}); err != nil {
			return fmt.Errorf("failed to patch webhook in CRD %q: %v", crdName, err)
		}

		log.Infof("Successfully patched webhook in CRD %q", crdName)
	}

	return nil
}

type nonLeaderRunnable struct {
	fn func(ctx context.Context) error
}

func (r nonLeaderRunnable) Start(ctx context.Context) error {
	return r.fn(ctx)
}

func (r nonLeaderRunnable) NeedLeaderElection() bool {
	return false
}
