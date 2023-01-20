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
	runtimeutil "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	componentsapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	configurationapi "github.com/dapr/dapr/pkg/apis/configuration/v1alpha1"
	resiliencyapi "github.com/dapr/dapr/pkg/apis/resiliency/v1alpha1"
	subscriptionsapiV1alpha1 "github.com/dapr/dapr/pkg/apis/subscriptions/v1alpha1"
	subscriptionsapiV2alpha1 "github.com/dapr/dapr/pkg/apis/subscriptions/v2alpha1"
	"github.com/dapr/dapr/pkg/health"
	"github.com/dapr/dapr/pkg/operator/api"
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
	Run(ctx context.Context)
}

// Options contains the options for `NewOperator`.
type Options struct {
	Config                    string
	LeaderElection            bool
	WatchdogEnabled           bool
	WatchdogInterval          time.Duration
	WatchdogMaxRestartsPerMin int
	SentryAddress             string
	ControlPlaneTrustDomain   string
	TrustAnchors              []byte
}

type operator struct {
	daprHandler *handlers.DaprHandler
	apiServer   api.Server

	mgr    ctrl.Manager
	sec    security.Interface
	client client.Client
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
func NewOperator(opts Options) Operator {
	conf, err := ctrl.GetConfig()
	if err != nil {
		log.Fatalf("Unable to get controller runtime configuration, err: %s", err)
	}

	client, err := client.New(conf, client.Options{Scheme: scheme})
	if err != nil {
		log.Fatalf("Unable to create client, err: %s", err)
	}

	config, err := LoadConfiguration(opts.Config, client)
	if err != nil {
		log.Fatalf("Unable to load configuration, err: %s", err)
	}

	sec, err := security.New(context.TODO(), security.Options{
		SentryAddress:           opts.SentryAddress,
		ControlPlaneTrustDomain: opts.ControlPlaneTrustDomain,
		ControlPlaneNamespace:   security.CurrentNamespace(),
		TrustAnchors:            opts.TrustAnchors,
		AppID:                   "dapr-operator",
		AppNamespace:            security.CurrentNamespace(),
		MTLSEnabled:             config.MTLSEnabled,
	})
	if err != nil {
		log.Fatalf("failed to create security: %s", err)
	}

	mgr, err := ctrl.NewManager(conf, ctrl.Options{
		Scheme:                 scheme,
		Port:                   19443,
		HealthProbeBindAddress: "0",
		MetricsBindAddress:     "0",
		LeaderElection:         opts.LeaderElection,
		LeaderElectionID:       "operator.dapr.io",
		TLSOpts:                []func(*tls.Config){sec.TLSServerConfigBasicTLSOption},
	})
	if err != nil {
		log.Fatalf("Unable to start manager, err: %s", err)
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
		}
		err = mgr.Add(wd)
		if err != nil {
			log.Fatalf("Unable to add watchdog controller, err: %s", err)
		}
	} else {
		log.Infof("Dapr Watchdog is not enabled")
	}

	daprHandler := handlers.NewDaprHandler(mgr)
	err = daprHandler.Init()
	if err != nil {
		log.Fatalf("Unable to initialize handler, err: %s", err)
	}

	o := &operator{
		daprHandler: daprHandler,
		mgr:         mgr,
		client:      mgrClient,
		sec:         sec,
		apiServer:   api.NewAPIServer(mgrClient),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	componentInformer, err := mgr.GetCache().GetInformer(ctx, &componentsapi.Component{})
	cancel()
	if err != nil {
		log.Fatalf("Unable to get setup components informer, err: %s", err)
	} else {
		componentInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
			AddFunc: o.syncComponent,
			UpdateFunc: func(_, newObj interface{}) {
				o.syncComponent(newObj)
			},
		})
	}

	return o
}

func (o *operator) syncComponent(obj interface{}) {
	c, ok := obj.(*componentsapi.Component)
	if ok {
		log.Debugf("Observed component to be synced, %s/%s", c.Namespace, c.Name)
		o.apiServer.OnComponentUpdated(c)
	}
}

func (o *operator) Run(ctx context.Context) {
	defer runtimeutil.HandleCrash()

	log.Infof("Dapr Operator is starting")

	go func() {
		if err := o.mgr.Start(ctx); err != nil {
			log.Fatalf("Failed to start controller manager, err: %s", err)
		}
	}()
	if !o.mgr.GetCache().WaitForCacheSync(ctx) {
		log.Fatalf("Failed to wait for cache sync")
	}

	/*
		Make sure to set `ENABLE_WEBHOOKS=false` when we run locally.
	*/
	if !strings.EqualFold(os.Getenv("ENABLE_WEBHOOKS"), "false") {
		if err := ctrl.NewWebhookManagedBy(o.mgr).
			For(&subscriptionsapiV1alpha1.Subscription{}).
			Complete(); err != nil {
			log.Fatalf("unable to create webhook Subscriptions v1alpha1: %v", err)
		}
		if err := ctrl.NewWebhookManagedBy(o.mgr).
			For(&subscriptionsapiV2alpha1.Subscription{}).
			Complete(); err != nil {
			log.Fatalf("unable to create webhook Subscriptions v2alpha1: %v", err)
		}
	}

	if err := o.patchCRDs(ctx, o.mgr.GetConfig(), "subscriptions.dapr.io"); err != nil {
		log.Fatal(err)
	}

	// start healthz server
	healthzServer := health.NewServer(log)
	go func() {
		// blocking call
		err := healthzServer.Run(ctx, healthzPort)
		if err != nil {
			log.Fatalf("Failed to start healthz server: %s", err)
		}
	}()

	// blocking call
	o.apiServer.Run(ctx, o.sec, func() {
		healthzServer.Ready()
		log.Infof("Dapr Operator started")
	})

	log.Infof("Dapr Operator is shutting down")
}

func (o *operator) patchCRDs(ctx context.Context, conf *rest.Config, crdNames ...string) error {
	clientSet, err := apiextensionsclient.NewForConfig(conf)
	if err != nil {
		return fmt.Errorf("Could not get API extension client: %v", err)
	}

	crdClient := clientSet.ApiextensionsV1().CustomResourceDefinitions()
	namespace := os.Getenv("NAMESPACE")
	if namespace == "" {
		return errors.New("Could not get dapr namespace")
	}

	for _, crdName := range crdNames {
		crd, err := crdClient.Get(ctx, crdName, v1.GetOptions{})
		if err != nil {
			log.Errorf("Could not get CRD %q: %v", crdName, err)
			continue
		}

		if crd == nil ||
			crd.Spec.Conversion == nil ||
			crd.Spec.Conversion.Webhook == nil ||
			crd.Spec.Conversion.Webhook.ClientConfig == nil {
			log.Errorf("CRD %q does not have an existing webhook client config. Applying resources of this type will fail.", crdName)

			continue
		}

		if crd.Spec.Conversion.Webhook.ClientConfig.Service != nil &&
			crd.Spec.Conversion.Webhook.ClientConfig.Service.Namespace == namespace &&
			crd.Spec.Conversion.Webhook.ClientConfig.CABundle != nil &&
			bytes.Equal(crd.Spec.Conversion.Webhook.ClientConfig.CABundle, o.sec.TrustAnchors()) {
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
			Value: namespace,
		}, {
			Op:    "replace",
			Path:  "/spec/conversion/webhook/clientConfig/caBundle",
			Value: o.sec.TrustAnchors(),
		}}

		payloadJSON, err := json.Marshal(payload)
		if err != nil {
			log.Errorf("Could not marshal webhook spec: %v", err)

			continue
		}
		_, err = crdClient.Patch(ctx, crdName, types.JSONPatchType, payloadJSON, v1.PatchOptions{})
		if err != nil {
			log.Errorf("Failed to patch webhook in CRD %q: %v", crdName, err)

			continue
		}

		log.Infof("Successfully patched webhook in CRD %q", crdName)
	}

	return nil
}
