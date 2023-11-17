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
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	clientfake "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	securityfake "github.com/dapr/dapr/pkg/security/fake"
	"github.com/dapr/kit/logger"
)

const (
	// TODO: Add sha256sum checks for linux, darwin, and windows & amd64, arm64.
	kubebuilderURL     = "https://storage.googleapis.com/kubebuilder-tools/kubebuilder-tools-%s-%s-%s.tar.gz"
	kubebuilderVersion = "1.28.0"
)

func Test_AddController(t *testing.T) {
	kubebuilderAssetBin := filepath.Join(os.TempDir(), "dapr_unit_tests")
	kubebuilderBinDir := filepath.Join(kubebuilderAssetBin, "kubebuilder/bin")

	if hasKubebuilderBins(t, kubebuilderBinDir) {
		t.Log("Kubebuilder bins already exist, skipping download...")
	} else {
		writeKubebuilderBins(t, kubebuilderAssetBin)
	}

	t.Setenv("KUBEBUILDER_ASSETS", kubebuilderBinDir)

	env := new(envtest.Environment)
	_, err := env.Start()
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, env.Stop())
	})

	cl, err := client.New(env.Config, client.Options{})
	require.NoError(t, err)

	currentBundle := []byte("root-ca-1")
	bundleUpdateCh := make(chan []byte)
	sec := securityfake.New().WithCurrentTrustAnchorsFn(func() ([]byte, error) {
		return currentBundle, nil
	}).WithWatchTrustAnchorsFn(func(ctx context.Context, tch chan<- []byte) {
		for {
			select {
			case upBundle := <-bundleUpdateCh:
				currentBundle = upBundle
				tch <- upBundle
			case <-ctx.Done():
				return
			}
		}
	})
	mngr, err := ctrl.NewManager(env.Config, ctrl.Options{})
	require.NoError(t, err)
	require.NoError(t, AddController(Options{Manager: mngr, Security: sec}))

	ctx, cancel := context.WithCancel(context.Background())
	mngrErrCh := make(chan error)
	t.Cleanup(func() {
		cancel()
		require.NoError(t, <-mngrErrCh)
	})

	t.Run("Current namespaces should have no dapr-root-ca.crt", func(t *testing.T) {
		var nsList corev1.NamespaceList
		require.NoError(t, cl.List(ctx, &nsList))
		assert.Len(t, nsList.Items, 4)
		for _, ns := range nsList.Items {
			assert.Contains(t, []string{"default", "kube-public", "kube-system", "kube-node-lease"}, ns.Name)
			require.True(t, apierrors.IsNotFound(
				cl.Get(ctx, client.ObjectKey{
					Namespace: ns.Name, Name: "dapr-root-ca.crt",
				}, new(corev1.ConfigMap))),
			)
		}
	})

	go func() {
		mngrErrCh <- mngr.Start(ctx)
	}()

	require.EventuallyWithT(t, func(t *assert.CollectT) {
		var nsList corev1.NamespaceList
		require.NoError(t, cl.List(ctx, &nsList))
		assert.Len(t, nsList.Items, 4)
		for _, ns := range nsList.Items {
			var cm corev1.ConfigMap
			//nolint:testifylint
			if assert.NoError(t, cl.Get(ctx, client.ObjectKey{
				Namespace: ns.Name, Name: "dapr-root-ca.crt",
			}, &cm)) {
				assert.Equal(t, map[string]string{"ca.crt": "root-ca-1"}, cm.Data)
			}
		}
	}, time.Second*10, time.Millisecond*100, "current namespaces should have dapr-root-ca.crt added")

	t.Run("Creating 2 new namespaces should have dapr-root-ca.crt added", func(t *testing.T) {
		require.NoError(t, cl.Create(ctx,
			&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "test-ns-1"}},
		))
		require.NoError(t, cl.Create(ctx,
			&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "test-ns-2"}},
		))

		var nsList corev1.NamespaceList
		require.NoError(t, cl.List(ctx, &nsList))
		assert.Len(t, nsList.Items, 6)
		require.EventuallyWithT(t, func(t *assert.CollectT) {
			for _, ns := range nsList.Items {
				var cm corev1.ConfigMap
				//nolint:testifylint
				if assert.NoError(t, cl.Get(ctx, client.ObjectKey{
					Namespace: ns.Name, Name: "dapr-root-ca.crt",
				}, &cm)) {
					assert.Equal(t, map[string]string{"ca.crt": "root-ca-1"}, cm.Data)
				}
			}
		}, time.Second*10, time.Millisecond*100, "new namespaces should have dapr-root-ca.crt added")
	})

	t.Run("Deleting ConfigMap should re-create it", func(t *testing.T) {
		var cm corev1.ConfigMap
		require.NoError(t, cl.Get(ctx,
			client.ObjectKey{Namespace: "test-ns-1", Name: "dapr-root-ca.crt"}, &cm),
		)
		require.NoError(t, cl.Delete(ctx, &cm))
		require.NoError(t, cl.Get(ctx,
			client.ObjectKey{Namespace: "test-ns-2", Name: "dapr-root-ca.crt"}, &cm),
		)
		require.NoError(t, cl.Delete(ctx, &cm))
		var nsList corev1.NamespaceList
		require.NoError(t, cl.List(ctx, &nsList))
		assert.Len(t, nsList.Items, 6)
		require.EventuallyWithT(t, func(t *assert.CollectT) {
			for _, ns := range nsList.Items {
				var cm corev1.ConfigMap
				require.NoError(t, cl.Get(ctx, client.ObjectKey{
					Namespace: ns.Name, Name: "dapr-root-ca.crt",
				}, &cm))
				assert.Equal(t, map[string]string{"ca.crt": "root-ca-1"}, cm.Data)
			}
		}, time.Second*10, time.Millisecond*100)
	})

	t.Run("Adding labels, annotations, and extra data to ConfigMap should not be overridden", func(t *testing.T) {
		for _, ns := range []string{"test-ns-1", "test-ns-2"} {
			var cm corev1.ConfigMap
			require.NoError(t, cl.Get(ctx, client.ObjectKey{
				Namespace: ns, Name: "dapr-root-ca.crt",
			}, &cm))
			cm.Labels = map[string]string{"foo": "bar"}
			cm.Annotations = map[string]string{"foo": "bar"}
			cm.Data["foo"] = "bar"
			delete(cm.Data, "ca.crt")
			require.NoError(t, cl.Update(ctx, &cm))
		}

		require.EventuallyWithT(t, func(t *assert.CollectT) {
			for _, ns := range []string{"test-ns-1", "test-ns-2"} {
				var cm corev1.ConfigMap
				require.NoError(t, cl.Get(ctx, client.ObjectKey{
					Namespace: ns, Name: "dapr-root-ca.crt",
				}, &cm))
				assert.Equal(t, map[string]string{
					"ca.crt": "root-ca-1",
					"foo":    "bar",
				}, cm.Data)
				assert.Equal(t, map[string]string{"foo": "bar"}, cm.Labels, "labels")
				assert.Equal(t, map[string]string{"foo": "bar"}, cm.Annotations, "annotations")
			}
		}, time.Second*10, time.Millisecond*100)
	})

	t.Run("Updating security TrustAnchors should update all ConfigMaps", func(t *testing.T) {
		bundleUpdateCh <- []byte("root-ca-1\nroot-ca-2")
		var nsList corev1.NamespaceList
		require.NoError(t, cl.List(ctx, &nsList))
		assert.Len(t, nsList.Items, 6)
		require.EventuallyWithT(t, func(t *assert.CollectT) {
			for _, ns := range nsList.Items {
				var cm corev1.ConfigMap
				require.NoError(t, cl.Get(ctx, client.ObjectKey{
					Namespace: ns.Name, Name: "dapr-root-ca.crt",
				}, &cm))
				assert.Equal(t, "root-ca-1\nroot-ca-2", cm.Data["ca.crt"])
			}
		}, time.Second*10, time.Millisecond*100)
	})

	t.Run("A Namespace which is Terminating should be ignored", func(t *testing.T) {
		require.NoError(t, cl.Delete(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "test-ns-1"}}))
		require.NoError(t, cl.Delete(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "test-ns-2"}}))

		require.EventuallyWithT(t, func(t *assert.CollectT) {
			for _, nsName := range []string{"test-ns-1", "test-ns-2"} {
				var ns corev1.Namespace
				require.NoError(t, cl.Get(ctx, client.ObjectKey{Name: nsName}, &ns))
				assert.Equal(t, corev1.NamespaceTerminating, ns.Status.Phase)
			}
		}, time.Second*10, time.Millisecond*100)

		bundleUpdateCh <- []byte("root-ca-2\nroot-ca-3")

		var nsList corev1.NamespaceList
		require.NoError(t, cl.List(ctx, &nsList))
		assert.Len(t, nsList.Items, 6)

		require.EventuallyWithT(t, func(t *assert.CollectT) {
			for _, ns := range nsList.Items {
				var cm corev1.ConfigMap
				require.NoError(t, cl.Get(ctx, client.ObjectKey{
					Namespace: ns.Name, Name: "dapr-root-ca.crt",
				}, &cm))
				if ns.Name == "test-ns-1" || ns.Name == "test-ns-2" {
					assert.Equal(t, "root-ca-1\nroot-ca-2", cm.Data["ca.crt"])
				} else {
					assert.Equal(t, "root-ca-2\nroot-ca-3", cm.Data["ca.crt"])
				}
			}
		}, time.Second*10, time.Millisecond*100)
	})
}

func writeKubebuilderBins(t *testing.T, assetBin string) {
	url := fmt.Sprintf(kubebuilderURL, kubebuilderVersion, runtime.GOOS, runtime.GOARCH)

	t.Logf("Downloading kubebuilder binaries to %q from %q...", assetBin, url)

	require.NoError(t, os.RemoveAll(assetBin))
	require.NoError(t, os.MkdirAll(assetBin, 0o700))

	req, err := http.NewRequest(http.MethodGet, url, nil)
	require.NoError(t, err)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, resp.Body.Close())
	})

	gzr, err := gzip.NewReader(resp.Body)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, gzr.Close())
	})

	tr := tar.NewReader(gzr)
	for {
		header, err := tr.Next()

		switch {
		case err == io.EOF:
			return
		case err != nil:
			require.NoError(t, err)
		case header == nil:
			continue
		}

		//nolint:gosec
		target := filepath.Join(assetBin, header.Name)

		switch header.Typeflag {
		case tar.TypeDir:
			if _, err := os.Stat(target); errors.Is(err, os.ErrNotExist) {
				require.NoError(t, os.MkdirAll(target, 0o700))
			} else {
				require.NoError(t, err)
			}

		case tar.TypeReg:
			f, err := os.OpenFile(target, os.O_CREATE|os.O_RDWR, os.FileMode(header.Mode))
			require.NoError(t, err)

			//nolint:gosec
			_, err = io.Copy(f, tr)
			require.NoError(t, err)
			require.NoError(t, f.Close())
		}
	}
}

func hasKubebuilderBins(t *testing.T, binDir string) bool {
	for _, bin := range []string{
		"etcd",
		"kube-apiserver",
		"kubectl",
	} {
		_, err := os.Stat(filepath.Join(binDir, bin))
		if errors.Is(err, os.ErrNotExist) {
			return false
		}
		require.NoError(t, err)
	}

	return true
}

func Test_Reconcile(t *testing.T) {
	const rootCAData = "root-ca"

	tests := map[string]struct {
		existingObjects []kruntime.Object
		expResult       ctrl.Result
		expError        bool
		expObjects      []kruntime.Object
	}{
		"if namespace doesn't exist, ignore": {
			existingObjects: nil,
			expResult:       ctrl.Result{},
			expError:        false,
			expObjects:      nil,
		},
		"if namespace is in a terminating state, ignore": {
			existingObjects: []kruntime.Object{
				&corev1.Namespace{
					TypeMeta:   metav1.TypeMeta{Kind: "Namespace", APIVersion: "v1"},
					ObjectMeta: metav1.ObjectMeta{Name: "test-ns", ResourceVersion: "10"},
					Status:     corev1.NamespaceStatus{Phase: corev1.NamespaceTerminating},
				},
			},
			expResult: ctrl.Result{},
			expError:  false,
			expObjects: []kruntime.Object{
				&corev1.Namespace{
					TypeMeta:   metav1.TypeMeta{Kind: "Namespace", APIVersion: "v1"},
					ObjectMeta: metav1.ObjectMeta{Name: "test-ns", ResourceVersion: "10"},
					Status:     corev1.NamespaceStatus{Phase: corev1.NamespaceTerminating},
				},
			},
		},
		"if namespace exists, but configmap doesn't, create config map": {
			existingObjects: []kruntime.Object{
				&corev1.Namespace{TypeMeta: metav1.TypeMeta{Kind: "Namespace", APIVersion: "v1"}, ObjectMeta: metav1.ObjectMeta{Name: "test-ns", ResourceVersion: "10"}},
			},
			expResult: ctrl.Result{},
			expError:  false,
			expObjects: []kruntime.Object{
				&corev1.Namespace{TypeMeta: metav1.TypeMeta{Kind: "Namespace", APIVersion: "v1"}, ObjectMeta: metav1.ObjectMeta{Name: "test-ns", ResourceVersion: "10"}},
				&corev1.ConfigMap{
					TypeMeta:   metav1.TypeMeta{Kind: "ConfigMap", APIVersion: "v1"},
					ObjectMeta: metav1.ObjectMeta{Name: "dapr-root-ca.crt", Namespace: "test-ns", ResourceVersion: "1"},
					Data:       map[string]string{"ca.crt": rootCAData},
				},
			},
		},
		"if namespace and configmap exists, but doesn't have any data, update with data": {
			existingObjects: []kruntime.Object{
				&corev1.Namespace{TypeMeta: metav1.TypeMeta{Kind: "Namespace", APIVersion: "v1"}, ObjectMeta: metav1.ObjectMeta{Name: "test-ns", ResourceVersion: "10"}},
				&corev1.ConfigMap{
					TypeMeta:   metav1.TypeMeta{Kind: "ConfigMap", APIVersion: "v1"},
					ObjectMeta: metav1.ObjectMeta{Name: "dapr-root-ca.crt", Namespace: "test-ns", ResourceVersion: "10"},
					Data:       nil,
				},
			},
			expResult: ctrl.Result{},
			expError:  false,
			expObjects: []kruntime.Object{
				&corev1.Namespace{TypeMeta: metav1.TypeMeta{Kind: "Namespace", APIVersion: "v1"}, ObjectMeta: metav1.ObjectMeta{Name: "test-ns", ResourceVersion: "10"}},
				&corev1.ConfigMap{
					TypeMeta:   metav1.TypeMeta{Kind: "ConfigMap", APIVersion: "v1"},
					ObjectMeta: metav1.ObjectMeta{Name: "dapr-root-ca.crt", Namespace: "test-ns", ResourceVersion: "11"},
					Data:       map[string]string{"ca.crt": rootCAData},
				},
			},
		},
		"if namespace and configmap exists, but doesn't have the right data, update with data": {
			existingObjects: []kruntime.Object{
				&corev1.Namespace{TypeMeta: metav1.TypeMeta{Kind: "Namespace", APIVersion: "v1"}, ObjectMeta: metav1.ObjectMeta{Name: "test-ns"}},
				&corev1.ConfigMap{
					TypeMeta:   metav1.TypeMeta{Kind: "ConfigMap", APIVersion: "v1"},
					ObjectMeta: metav1.ObjectMeta{Name: "dapr-root-ca.crt", Namespace: "test-ns", ResourceVersion: "10"},
					Data:       map[string]string{"ca.crt": "not-root-ca"},
				},
			},
			expResult: ctrl.Result{},
			expError:  false,
			expObjects: []kruntime.Object{
				&corev1.Namespace{TypeMeta: metav1.TypeMeta{Kind: "Namespace", APIVersion: "v1"}, ObjectMeta: metav1.ObjectMeta{Name: "test-ns", ResourceVersion: "999"}},
				&corev1.ConfigMap{
					TypeMeta:   metav1.TypeMeta{Kind: "ConfigMap", APIVersion: "v1"},
					ObjectMeta: metav1.ObjectMeta{Name: "dapr-root-ca.crt", Namespace: "test-ns", ResourceVersion: "11"},
					Data:       map[string]string{"ca.crt": rootCAData},
				},
			},
		},
		"if namespace and configmap exists with correct data but with extra keys, don't remove extra keys": {
			existingObjects: []kruntime.Object{
				&corev1.Namespace{TypeMeta: metav1.TypeMeta{Kind: "Namespace", APIVersion: "v1"}, ObjectMeta: metav1.ObjectMeta{Name: "test-ns"}},
				&corev1.ConfigMap{
					TypeMeta:   metav1.TypeMeta{Kind: "ConfigMap", APIVersion: "v1"},
					ObjectMeta: metav1.ObjectMeta{Name: "dapr-root-ca.crt", Namespace: "test-ns", ResourceVersion: "10"},
					Data:       map[string]string{"ca.crt": rootCAData, "foo": "bar"},
				},
			},
			expResult: ctrl.Result{},
			expError:  false,
			expObjects: []kruntime.Object{
				&corev1.Namespace{TypeMeta: metav1.TypeMeta{Kind: "Namespace", APIVersion: "v1"}, ObjectMeta: metav1.ObjectMeta{Name: "test-ns", ResourceVersion: "999"}},
				&corev1.ConfigMap{
					TypeMeta:   metav1.TypeMeta{Kind: "ConfigMap", APIVersion: "v1"},
					ObjectMeta: metav1.ObjectMeta{Name: "dapr-root-ca.crt", Namespace: "test-ns", ResourceVersion: "10"},
					Data:       map[string]string{"ca.crt": rootCAData, "foo": "bar"},
				},
			},
		},
		"if namespace and configmap exists with correct data, do nothing": {
			existingObjects: []kruntime.Object{
				&corev1.Namespace{TypeMeta: metav1.TypeMeta{Kind: "Namespace", APIVersion: "v1"}, ObjectMeta: metav1.ObjectMeta{Name: "test-ns", ResourceVersion: "10"}},
				&corev1.ConfigMap{
					TypeMeta:   metav1.TypeMeta{Kind: "ConfigMap", APIVersion: "v1"},
					ObjectMeta: metav1.ObjectMeta{Name: "dapr-root-ca.crt", Namespace: "test-ns", ResourceVersion: "10"},
					Data:       map[string]string{"ca.crt": rootCAData},
				},
			},
			expResult: ctrl.Result{},
			expError:  false,
			expObjects: []kruntime.Object{
				&corev1.Namespace{TypeMeta: metav1.TypeMeta{Kind: "Namespace", APIVersion: "v1"}, ObjectMeta: metav1.ObjectMeta{Name: "test-ns", ResourceVersion: "10"}},
				&corev1.ConfigMap{
					TypeMeta:   metav1.TypeMeta{Kind: "ConfigMap", APIVersion: "v1"},
					ObjectMeta: metav1.ObjectMeta{Name: "dapr-root-ca.crt", Namespace: "test-ns", ResourceVersion: "10"},
					Data:       map[string]string{"ca.crt": rootCAData},
				},
			},
		},
		"if namespace and configmap exists with correct data and extra labels, do nothing": {
			existingObjects: []kruntime.Object{
				&corev1.Namespace{TypeMeta: metav1.TypeMeta{Kind: "Namespace", APIVersion: "v1"}, ObjectMeta: metav1.ObjectMeta{Name: "test-ns", ResourceVersion: "10"}},
				&corev1.ConfigMap{
					TypeMeta:   metav1.TypeMeta{Kind: "ConfigMap", APIVersion: "v1"},
					ObjectMeta: metav1.ObjectMeta{Name: "dapr-root-ca.crt", Namespace: "test-ns", ResourceVersion: "10", Labels: map[string]string{"foo": "bar"}},
					Data:       map[string]string{"ca.crt": rootCAData},
				},
			},
			expResult: ctrl.Result{},
			expError:  false,
			expObjects: []kruntime.Object{
				&corev1.Namespace{TypeMeta: metav1.TypeMeta{Kind: "Namespace", APIVersion: "v1"}, ObjectMeta: metav1.ObjectMeta{Name: "test-ns", ResourceVersion: "10"}},
				&corev1.ConfigMap{
					TypeMeta:   metav1.TypeMeta{Kind: "ConfigMap", APIVersion: "v1"},
					ObjectMeta: metav1.ObjectMeta{Name: "dapr-root-ca.crt", Namespace: "test-ns", ResourceVersion: "10", Labels: map[string]string{"foo": "bar"}},
					Data:       map[string]string{"ca.crt": rootCAData},
				},
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			cl := clientfake.NewClientBuilder().
				WithRuntimeObjects(test.existingObjects...).
				Build()

			trust := &trust{
				client: cl,
				lister: cl,
				log:    logger.NewLogger("dapr.operator.trust"),
				security: securityfake.New().WithCurrentTrustAnchorsFn(func() ([]byte, error) {
					return []byte(rootCAData), nil
				}),
			}

			result, err := trust.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "test-ns", Name: "dapr-root-ca.crt"}})
			assert.Equalf(t, test.expError, err != nil, "%v", err)
			assert.Equal(t, test.expResult, result)

			for _, expectedObject := range test.expObjects {
				expObj := expectedObject.(client.Object)
				var actual client.Object
				switch expObj.(type) {
				case *corev1.ConfigMap:
					actual = &corev1.ConfigMap{}
				case *corev1.Namespace:
					actual = &corev1.Namespace{}
				default:
					t.Errorf("unexpected object kind in expected: %#+v", expObj)
				}

				err := cl.Get(context.Background(), client.ObjectKeyFromObject(expObj), actual)
				require.NoError(t, err)
				assert.True(t, equality.Semantic.DeepEqual(expObj, actual), "expected object: %#+v, actual object: %#+v", expObj, actual)
			}
		})
	}
}
