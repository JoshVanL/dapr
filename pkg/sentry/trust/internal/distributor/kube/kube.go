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

package kube

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	kubernetesscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"github.com/dapr/dapr/pkg/sentry/trust/internal"
	"github.com/dapr/kit/logger"
)

const (
	// cmRootCAName is the name of the ConfigMap that contains the root CA.
	cmRootCAName = "dapr-root-ca.crt"

	// cmRootCAFile is the name of the file that contains the root CA inside the
	// ConfigMap.
	cmRootCAFile = "ca.crt"
)

// Options configures a Kubernetes trust Distributor.
type Options struct {
	RestConfig *rest.Config
	Namespace  string
	Source     internal.Source
}

// kubernetes is the Kubernetes implementation of the trust Distributor. It
// watches for trust bundle updates, and distributes the trust bundle to all
// dapr trust bundle ConfigMaps in the cluster.
type kubernetes struct {
	client  client.Client
	source  internal.Source
	mngr    ctrl.Manager
	running atomic.Bool
}

// New creates a new Kubernetes trust Distributor with the given options.
func New(opts Options) (internal.Distributor, error) {
	scheme := runtime.NewScheme()
	if err := kubernetesscheme.AddToScheme(scheme); err != nil {
		return nil, err
	}
	client, err := client.New(opts.RestConfig, client.Options{
		Scheme: scheme,
	})

	mngr, err := ctrl.NewManager(opts.RestConfig, ctrl.Options{
		Scheme:                        scheme,
		LeaderElection:                true,
		LeaderElectionID:              "configmap.distributor.trust.sentry.dapr.io",
		LeaderElectionNamespace:       opts.Namespace,
		LeaderElectionReleaseOnCancel: true,
		HealthProbeBindAddress:        "0",
		MetricsBindAddress:            "0",
		NewCache: cache.BuilderWithOptions(cache.Options{
			Scheme: scheme,
			SelectorsByObject: cache.SelectorsByObject{
				new(corev1.ConfigMap): {
					Field: fields.SelectorFromSet(fields.Set{"metadata.name": cmRootCAFile}),
				},
				new(corev1.Namespace): {},
			},
		}),
	})
	if err != nil {
		return nil, err
	}

	return (&kubernetes{
		client: client,
		source: opts.Source,
		mngr:   mngr,
	}).Run, nil
}

// Run runs the Kubernetes trust Distributor.
func (k *kubernetes) Run(ctx context.Context) error {
	if !k.running.CompareAndSwap(false, true) {
		return errors.New("kubernetes trust distributor already running")
	}

	trustAnchorChan := make(chan event.GenericEvent)

	err := k.mngr.Add(manager.RunnableFunc(func(ctx context.Context) error {
		log := logger.NewLogger("dapr.sentry.trust.distributor.kubernetes")
		log.Info("Starting Kubernetes trust distributor")
		tdChan := k.source.Subscribe()

		// Wait for initial bundle to be available then sync all namespaces.
		if _, err := k.source.Latest(ctx); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case trustAnchorChan <- event.GenericEvent{}:
		}

		for {
			select {
			case <-ctx.Done():
				return nil
			case <-tdChan:
				trustAnchorChan <- event.GenericEvent{}
			}
		}
	}))
	if err != nil {
		return err
	}

	err = ctrl.NewControllerManagedBy(k.mngr).
		Named("configmap.distributor.trust.sentry.dapr.io").
		For(new(corev1.ConfigMap), builder.OnlyMetadata).
		Watches(&source.Kind{Type: new(corev1.Namespace)}, handler.EnqueueRequestsFromMapFunc(
			func(obj client.Object) []reconcile.Request {
				return []ctrl.Request{{NamespacedName: types.NamespacedName{Namespace: obj.GetName(), Name: cmRootCAName}}}
			},
		), builder.OnlyMetadata).
		Watches(&source.Channel{Source: trustAnchorChan}, handler.EnqueueRequestsFromMapFunc(
			func(obj client.Object) []reconcile.Request {
				var nss corev1.NamespaceList
				if err := k.mngr.GetCache().List(ctx, &nss); err != nil {
					return nil
				}
				reqs := make([]reconcile.Request, len(nss.Items))
				for i := range nss.Items {
					reqs[i] = reconcile.Request{NamespacedName: types.NamespacedName{Namespace: nss.Items[i].GetName(), Name: cmRootCAName}}
				}
				return reqs
			},
		)).
		Complete(reconcile.Func(k.reconcile))
	if err != nil {
		return err
	}

	return k.mngr.Start(ctx)
}

// reconcile reconciles the Kubernetes trust Distributor. It ensures that all
// dapr trust bundle ConfigMaps in the cluster are up to date with the latest
// trust bundle.
func (k *kubernetes) reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logger.NewLogger("dapr.sentry.trust.distributor.kubernetes")
	log.Infof("Reconciling Kubernetes trust distributor: %s", req.NamespacedName)
	// Only reconcile ConfigMaps named dapr-root-ca.crt.
	if req.Name != cmRootCAName {
		return ctrl.Result{}, nil
	}

	latest, err := k.source.Latest(ctx)
	if err != nil {
		return ctrl.Result{}, err
	}
	latestStr := latest.PEMString()

	var cm corev1.ConfigMap
	err = k.client.Get(ctx, types.NamespacedName{Namespace: req.Namespace, Name: req.Name}, &cm)
	// ConfigMap does not exist, create it.
	if apierrors.IsNotFound(err) {
		return ctrl.Result{}, k.client.Create(ctx, &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      cmRootCAName,
				Namespace: req.Namespace,
			},
			Data: map[string]string{
				cmRootCAFile: latestStr,
			},
		})
	}

	if err != nil {
		return ctrl.Result{}, err
	}

	fmt.Printf("Current: %s\nLatest: %s\n", cm.Data[cmRootCAFile], latestStr)
	// ConfigMap exists, update it if necessary.
	if cm.Data[cmRootCAFile] != latestStr {
		cm.Data[cmRootCAFile] = latestStr
		return ctrl.Result{}, k.client.Update(ctx, &cm)
	}

	return ctrl.Result{}, nil
}
