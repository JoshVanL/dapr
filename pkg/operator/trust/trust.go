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

package trust

import (
	"context"
	"fmt"
	"os"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/dapr/pkg/security/consts"
	"github.com/dapr/kit/logger"
)

// Based from https://github.com/cert-manager/istio-csr/blob/v0.7.1/pkg/controller/configmap.go

type Options struct {
	// Manager is the controller-runtime Manager that the controller will be
	// registered against.
	Manager manager.Manager

	// Security exposes Trust Anchor updates.
	Security security.Provider
}

// trust is the controller that is responsible for ensuring that all
// namespaces have the correct ConfigMap with the Dapr root CA
type trust struct {
	// client is a Kubernetes client that makes calls to the API for every
	// request.
	// Should be used for creating and updating resources.
	// This is a separate delegating client which doesn't cache ConfigMaps, see
	// https://github.com/kubernetes-sigs/controller-runtime/issues/1454
	client client.Client

	// lister makes requests to the informer cache. Beware that resources who's
	// informer only caches metadata, will not return underlying data of that
	// resource. Use client instead.
	lister client.Reader

	security security.Provider

	log       logger.Logger
	caEventCh chan event.GenericEvent
}

func AddController(opts Options) error {
	// noCacheClient is used to retrieve objects that we don't want to cache.
	noCacheClient, err := client.New(opts.Manager.GetConfig(), client.Options{
		Cache: &client.CacheOptions{
			Reader:     opts.Manager.GetCache(),
			DisableFor: []client.Object{new(corev1.ConfigMap)},
		},
	})
	if err != nil {
		return fmt.Errorf("failed to build non-cached client for ConfigMaps: %w", err)
	}

	t := &trust{
		client:    noCacheClient,
		lister:    opts.Manager.GetCache(),
		log:       logger.NewLogger("dapr.operator.trust"),
		security:  opts.Security,
		caEventCh: make(chan event.GenericEvent),
	}

	if err = opts.Manager.Add(manager.RunnableFunc(t.run)); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(opts.Manager).
		For(new(corev1.ConfigMap), builder.OnlyMetadata, builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
			return obj.GetName() == consts.DaprRootCAConfigMapName
		}))).

		// Watch all Namespaces. Cache whole Namespace to include Phase Status.
		Watches(&corev1.Namespace{}, handler.EnqueueRequestsFromMapFunc(
			func(_ context.Context, obj client.Object) []reconcile.Request {
				return []reconcile.Request{
					{NamespacedName: types.NamespacedName{Namespace: obj.GetName(), Name: consts.DaprRootCAConfigMapName}},
				}
			},
		)).

		// If the CA roots changes then reconcile all ConfigMaps
		WatchesRawSource(&source.Channel{Source: t.caEventCh}, handler.EnqueueRequestsFromMapFunc(
			func(ctx context.Context, obj client.Object) []reconcile.Request {
				var namespaceList corev1.NamespaceList
				if err := t.lister.List(ctx, &namespaceList); err != nil {
					// TODO: @joshvanl
					t.log.Error("failed to list namespaces, exiting...: %s", err)
					os.Exit(0)
				}
				var requests []reconcile.Request
				for _, namespace := range namespaceList.Items {
					requests = append(requests, ctrl.Request{NamespacedName: types.NamespacedName{Namespace: namespace.Name, Name: consts.DaprRootCAConfigMapName}})
				}
				return requests
			},
		)).

		// Complete controller.
		Complete(t)
}

func (t *trust) run(ctx context.Context) error {
	sec, err := t.security.Handler(ctx)
	if err != nil {
		return err
	}

	taCh := make(chan []byte)
	closeCh := make(chan struct{})
	go func() {
		defer close(closeCh)
		sec.WatchTrustAnchors(ctx, taCh)
	}()

	for {
		select {
		case <-ctx.Done():
			<-closeCh
			return nil
		case <-taCh:
			t.caEventCh <- event.GenericEvent{}
		}
	}
}

// Reconcile is the main ConfigMap Reconcile loop.
// It will ensure that the Dapr root CA is present in all namespaces.
func (t *trust) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := t.log.WithFields(map[string]any{
		"namespace": req.Namespace,
		"configmap": req.Name,
	})
	log.Debug("syncing configmap")

	// Check Namespace is not deleted and is not in a terminating state.
	var namespace corev1.Namespace
	err := t.lister.Get(ctx, client.ObjectKey{Name: req.Namespace}, &namespace)
	if apierrors.IsNotFound(err) {
		// No need to reconcile the configmap if the namespace is deleted.
		log.Debug("namespace does not exist")
		return ctrl.Result{}, nil
	}
	if err != nil {
		return ctrl.Result{}, err
	}

	if namespace.Status.Phase == corev1.NamespaceTerminating {
		log.WithFields(map[string]any{"phase": corev1.NamespaceTerminating}).Info("skipping sync for namespace as it is terminating")
		return ctrl.Result{}, nil
	}

	sec, err := t.security.Handler(ctx)
	if err != nil {
		return ctrl.Result{}, err
	}
	rootCAsPEMB, err := sec.CurrentTrustAnchors()
	if err != nil {
		return ctrl.Result{}, err
	}
	rootCAsPEM := string(rootCAsPEMB)

	// Check ConfigMap exists, and has the correct data.
	var configMap corev1.ConfigMap
	err = t.client.Get(ctx, client.ObjectKey{Namespace: req.Namespace, Name: consts.DaprRootCAConfigMapName}, &configMap)

	// If the ConfigMap doesn't exist, create it with the correct data
	if apierrors.IsNotFound(err) {
		log.Info("creating configmap with root CA data")
		return ctrl.Result{}, t.client.Create(ctx, &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: req.Namespace,
				Name:      consts.DaprRootCAConfigMapName,
			},
			Data: map[string]string{
				"ca.crt": rootCAsPEM,
			},
		})
	}

	if err != nil {
		return ctrl.Result{}, err
	}

	orig := configMap.DeepCopy()
	if configMap.Data == nil {
		configMap.Data = make(map[string]string)
	}

	// If the ConfigMap doesn't have the expected key value, update.
	if data, ok := configMap.Data["ca.crt"]; !ok || data != rootCAsPEM {
		// TODO: append to existing data
		configMap.Data["ca.crt"] = rootCAsPEM
		log.Info("updating ConfigMap data")
		return ctrl.Result{}, t.client.Patch(ctx, &configMap, client.MergeFrom(orig))
	}

	log.Debug("no update needed")

	return ctrl.Result{}, nil
}
