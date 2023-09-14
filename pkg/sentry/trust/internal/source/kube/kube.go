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
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	kubernetesscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	kcache "k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/dapr/dapr/pkg/concurrency"
	"github.com/dapr/dapr/pkg/sentry/trust/internal"
	"github.com/dapr/kit/logger"
)

type SourceKind string

const (
	SourceKindSecret    SourceKind = "Secret"
	SourceKindConfigMap SourceKind = "ConfigMap"
)

// Options configures a Kubernetes trust Source.
type Options struct {
	RestConfig   *rest.Config
	Namespace    string
	ResourceName string
	SourceKind   SourceKind
}

// kubernetes is the Kubernetes implementation of the trust Source. It watches
// for trust bundle updates and reports them to subscribers.
type kubernetes struct {
	sourceKind  client.Object
	cache       cache.Cache
	subscribers []chan<- *internal.TrustAnchors
	log         logger.Logger

	initialFound chan struct{}
	current      *internal.TrustAnchors
	haveSum      map[[sha256.Size224]byte]bool

	lock    sync.RWMutex
	running atomic.Bool
	closeCh chan struct{}
	wg      sync.WaitGroup
}

func New(opts Options) (internal.Source, error) {
	scheme := runtime.NewScheme()
	if err := kubernetesscheme.AddToScheme(scheme); err != nil {
		return nil, err
	}

	var obj client.Object
	switch opts.SourceKind {
	case SourceKindSecret:
		obj = new(corev1.Secret)
	case SourceKindConfigMap:
		obj = new(corev1.ConfigMap)
	default:
		return nil, fmt.Errorf("unknown target kind %q", opts.SourceKind)
	}

	ch, err := cache.New(opts.RestConfig, cache.Options{
		Scheme:    scheme,
		Namespace: opts.Namespace,
		SelectorsByObject: cache.SelectorsByObject{
			obj: {
				Field: fields.SelectorFromSet(fields.Set{"metadata.name": opts.ResourceName}),
			},
		},
	})
	if err != nil {
		return nil, err
	}

	k := &kubernetes{
		cache:        ch,
		sourceKind:   obj,
		initialFound: make(chan struct{}),
		haveSum:      make(map[[sha256.Size224]byte]bool),
		closeCh:      make(chan struct{}),
		log: logger.NewLogger("dapr.sentry.trust.source.kubernetes." +
			strings.ToLower(string(opts.SourceKind)),
		),
	}

	return k, nil
}

func (k *kubernetes) Run(ctx context.Context) error {
	if !k.running.CompareAndSwap(false, true) {
		return errors.New("kubernetes trust source already running")
	}

	err := concurrency.NewRunnerManager(
		k.cache.Start,
		func(ctx context.Context) error {
			defer close(k.closeCh)

			inf, err := k.cache.GetInformer(ctx, k.sourceKind)
			if err != nil {
				return err
			}
			_, err = inf.AddEventHandlerWithResyncPeriod(kcache.ResourceEventHandlerFuncs{
				AddFunc: k.reconcile,
				UpdateFunc: func(_, obj any) {
					k.reconcile(obj)
				},
				DeleteFunc: k.reconcile,
			}, time.Minute)
			if err != nil {
				return err
			}

			if !k.cache.WaitForCacheSync(ctx) {
				return ctx.Err()
			}

			<-ctx.Done()
			return nil
		},
	).Run(ctx)

	k.wg.Wait()

	return err
}

func (k *kubernetes) Latest(ctx context.Context) (*internal.TrustAnchors, error) {
	select {
	case <-k.initialFound:
		k.lock.RLock()
		defer k.lock.RUnlock()
		return k.current.DeepCopy(), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (k *kubernetes) Subscribe() internal.TrustAnchorsChannel {
	k.lock.Lock()
	defer k.lock.Unlock()
	ch := make(chan *internal.TrustAnchors)
	k.subscribers = append(k.subscribers, ch)

	// Send the current value to the new subscriber if we have one.
	select {
	case <-k.initialFound:
		k.sendCurrentToChannel(ch, k.current.DeepCopy())
	default:
	}

	return ch
}

func (k *kubernetes) reconcile(obj any) {
	k.lock.Lock()
	defer k.lock.Unlock()

	var pemCerts []byte
	switch v := obj.(type) {
	case *corev1.Secret:
		pemCerts = v.Data["ca.crt"]
	case *corev1.ConfigMap:
		pemCerts = []byte(v.Data["ca.crt"])
	default:
		k.log.Errorf("unexpected object type %T", obj)
		return
	}

	var updated bool
	for len(pemCerts) > 0 {
		var block *pem.Block
		block, pemCerts = pem.Decode(pemCerts)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
			continue
		}

		certBytes := block.Bytes
		cert, err := x509.ParseCertificate(certBytes)
		if err != nil {
			continue
		}

		key := sha256.Sum224(cert.Raw)
		if k.haveSum[key] {
			continue
		}

		k.haveSum[key] = true
		if k.current == nil {
			k.current = internal.NewTrustAnchors()
		}
		k.current.AppendPEM(pem.EncodeToMemory(&pem.Block{
			Type:  "CERTIFICATE",
			Bytes: certBytes,
		}))
		updated = true
	}

	// If we have updated the current value, send it to all subscribers. Signal
	// that the initial value has been found if it has not been found yet.
	if updated {
		select {
		case <-k.initialFound:
		default:
			close(k.initialFound)
		}
		for _, ch := range k.subscribers {
			k.sendCurrentToChannel(ch, k.current.DeepCopy())
		}
	}
}

func (k *kubernetes) sendCurrentToChannel(ch chan<- *internal.TrustAnchors, current *internal.TrustAnchors) {
	k.wg.Add(1)
	go func() {
		defer k.wg.Done()
		select {
		case <-k.closeCh:
		case ch <- current:
		}
	}()
}
