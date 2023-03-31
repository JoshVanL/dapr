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

package kubernetes

import (
	"context"
	"fmt"
	"testing"

	"github.com/golang-jwt/jwt"
	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	kauthapi "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	kubefake "k8s.io/client-go/kubernetes/fake"
	core "k8s.io/client-go/testing"
	clientfake "sigs.k8s.io/controller-runtime/pkg/client/fake"

	configapi "github.com/dapr/dapr/pkg/apis/configuration/v1alpha1"
	sentryv1pb "github.com/dapr/dapr/pkg/proto/sentry/v1"
)

func TestValidate(t *testing.T) {
	newToken := func(podName string) string {
		return jwt.EncodeSegment([]byte(`{"alg":"RS256"}`)) + "." +
			jwt.EncodeSegment([]byte(fmt.Sprintf(`{"kubernetes.io":{"pod":{"name": "%s"}}}`, podName))) + "." +
			jwt.EncodeSegment([]byte("{}"))
	}

	sentryID := spiffeid.RequireFromPath(spiffeid.RequireTrustDomainFromString("cluster.local"), "/ns/dapr-test/dapr-sentry")

	tests := map[string]struct {
		reactor func(t *testing.T) core.ReactionFunc
		req     *sentryv1pb.SignCertificateRequest
		pod     *corev1.Pod
		config  *configapi.Configuration

		sentryAudience         string
		noDefaultTokenAudience bool

		expTD  spiffeid.TrustDomain
		expErr bool
	}{
		"if pod in different namespace, expect error": {
			noDefaultTokenAudience: true,
			sentryAudience:         "spiffe://cluster.local/ns/dapr-test/dapr-sentry",
			reactor: func(t *testing.T) core.ReactionFunc {
				return func(action core.Action) (bool, runtime.Object, error) {
					obj := action.(core.CreateAction).GetObject().(*kauthapi.TokenReview)
					assert.Equal(t, []string{"dapr.io/sentry", "spiffe://cluster.local/ns/dapr-test/dapr-sentry"}, obj.Spec.Audiences)
					return true, &kauthapi.TokenReview{Status: kauthapi.TokenReviewStatus{
						Authenticated: true,
						User: kauthapi.UserInfo{
							Username: "system:serviceaccount:my-ns:my-sa",
						},
					}}, nil
				}
			},
			req: &sentryv1pb.SignCertificateRequest{
				CertificateSigningRequest: []byte("csr"),
				Namespace:                 "my-ns",
				Token:                     newToken("my-pod"),
				TrustDomain:               "example.test.dapr.io",
				Id:                        "my-app-id",
			},
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-pod",
					Namespace: "not-my-ns",
					Annotations: map[string]string{
						"dapr.io/app-id": "my-app-id",
						"dapr.io/config": "my-config",
					},
				},
				Spec: corev1.PodSpec{ServiceAccountName: "my-sa"},
			},
			config: &configapi.Configuration{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-config",
					Namespace: "my-ns",
				},
				Spec: configapi.ConfigurationSpec{
					AccessControlSpec: configapi.AccessControlSpec{
						TrustDomain: "",
					},
				},
			},
			expErr: true,
			expTD:  spiffeid.TrustDomain{},
		},

		"if config in different namespace, expect error": {
			noDefaultTokenAudience: true,
			sentryAudience:         "spiffe://cluster.local/ns/dapr-test/dapr-sentry",
			reactor: func(t *testing.T) core.ReactionFunc {
				return func(action core.Action) (bool, runtime.Object, error) {
					obj := action.(core.CreateAction).GetObject().(*kauthapi.TokenReview)
					assert.Equal(t, []string{"dapr.io/sentry", "spiffe://cluster.local/ns/dapr-test/dapr-sentry"}, obj.Spec.Audiences)
					return true, &kauthapi.TokenReview{Status: kauthapi.TokenReviewStatus{
						Authenticated: true,
						User: kauthapi.UserInfo{
							Username: "system:serviceaccount:my-ns:my-sa",
						},
					}}, nil
				}
			},
			req: &sentryv1pb.SignCertificateRequest{
				CertificateSigningRequest: []byte("csr"),
				Namespace:                 "my-ns",
				Token:                     newToken("my-pod"),
				TrustDomain:               "example.test.dapr.io",
				Id:                        "my-app-id",
			},
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-pod",
					Namespace: "my-ns",
					Annotations: map[string]string{
						"dapr.io/app-id": "my-app-id",
						"dapr.io/config": "my-config",
					},
				},
				Spec: corev1.PodSpec{ServiceAccountName: "my-sa"},
			},
			config: &configapi.Configuration{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-config",
					Namespace: "not-my-ns",
				},
				Spec: configapi.ConfigurationSpec{
					AccessControlSpec: configapi.AccessControlSpec{
						TrustDomain: "",
					},
				},
			},
			expErr: true,
			expTD:  spiffeid.TrustDomain{},
		},

		"is missing app-id and app-id is the same as the pod name, expect no error": {
			noDefaultTokenAudience: true,
			sentryAudience:         "spiffe://cluster.local/ns/dapr-test/dapr-sentry",
			reactor: func(t *testing.T) core.ReactionFunc {
				return func(action core.Action) (bool, runtime.Object, error) {
					obj := action.(core.CreateAction).GetObject().(*kauthapi.TokenReview)
					assert.Equal(t, []string{"dapr.io/sentry", "spiffe://cluster.local/ns/dapr-test/dapr-sentry"}, obj.Spec.Audiences)
					return true, &kauthapi.TokenReview{Status: kauthapi.TokenReviewStatus{
						Authenticated: true,
						User: kauthapi.UserInfo{
							Username: "system:serviceaccount:my-ns:my-sa",
						},
					}}, nil
				}
			},
			req: &sentryv1pb.SignCertificateRequest{
				CertificateSigningRequest: []byte("csr"),
				Namespace:                 "my-ns",
				Token:                     newToken("my-pod"),
				TrustDomain:               "example.test.dapr.io",
				Id:                        "my-pod",
			},
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-pod",
					Namespace: "my-ns",
					Annotations: map[string]string{
						"dapr.io/config": "my-config",
					},
				},
				Spec: corev1.PodSpec{ServiceAccountName: "my-sa"},
			},
			config: &configapi.Configuration{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-config",
					Namespace: "my-ns",
				},
				Spec: configapi.ConfigurationSpec{
					AccessControlSpec: configapi.AccessControlSpec{
						TrustDomain: "",
					},
				},
			},
			expErr: false,
			expTD:  spiffeid.RequireTrustDomainFromString("public"),
		},

		"if missing app-id annotation and app-id doesn't match pod name, expect error": {
			noDefaultTokenAudience: true,
			sentryAudience:         "spiffe://cluster.local/ns/dapr-test/dapr-sentry",
			reactor: func(t *testing.T) core.ReactionFunc {
				return func(action core.Action) (bool, runtime.Object, error) {
					obj := action.(core.CreateAction).GetObject().(*kauthapi.TokenReview)
					assert.Equal(t, []string{"dapr.io/sentry", "spiffe://cluster.local/ns/dapr-test/dapr-sentry"}, obj.Spec.Audiences)
					return true, &kauthapi.TokenReview{Status: kauthapi.TokenReviewStatus{
						Authenticated: true,
						User: kauthapi.UserInfo{
							Username: "system:serviceaccount:my-ns:my-sa",
						},
					}}, nil
				}
			},
			req: &sentryv1pb.SignCertificateRequest{
				CertificateSigningRequest: []byte("csr"),
				Namespace:                 "my-ns",
				Token:                     newToken("my-pod"),
				TrustDomain:               "example.test.dapr.io",
				Id:                        "my-app-id",
			},
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-pod",
					Namespace: "my-ns",
					Annotations: map[string]string{
						"dapr.io/config": "my-config",
					},
				},
				Spec: corev1.PodSpec{ServiceAccountName: "my-sa"},
			},
			config: &configapi.Configuration{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-config",
					Namespace: "my-ns",
				},
				Spec: configapi.ConfigurationSpec{
					AccessControlSpec: configapi.AccessControlSpec{
						TrustDomain: "",
					},
				},
			},
			expErr: true,
			expTD:  spiffeid.TrustDomain{},
		},

		"if target configuration doesn't exist, expect error": {
			noDefaultTokenAudience: true,
			sentryAudience:         "spiffe://cluster.local/ns/dapr-test/dapr-sentry",
			reactor: func(t *testing.T) core.ReactionFunc {
				return func(action core.Action) (bool, runtime.Object, error) {
					obj := action.(core.CreateAction).GetObject().(*kauthapi.TokenReview)
					assert.Equal(t, []string{"dapr.io/sentry", "spiffe://cluster.local/ns/dapr-test/dapr-sentry"}, obj.Spec.Audiences)
					return true, &kauthapi.TokenReview{Status: kauthapi.TokenReviewStatus{
						Authenticated: true,
						User: kauthapi.UserInfo{
							Username: "system:serviceaccount:my-ns:my-sa",
						},
					}}, nil
				}
			},
			req: &sentryv1pb.SignCertificateRequest{
				CertificateSigningRequest: []byte("csr"),
				Namespace:                 "my-ns",
				Token:                     newToken("my-pod"),
				TrustDomain:               "example.test.dapr.io",
				Id:                        "my-app-id",
			},
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-pod",
					Namespace: "my-ns",
					Annotations: map[string]string{
						"dapr.io/app-id": "my-app-id",
						"dapr.io/config": "my-config",
					},
				},
				Spec: corev1.PodSpec{ServiceAccountName: "my-sa"},
			},
			config: nil,
			expErr: true,
			expTD:  spiffeid.TrustDomain{},
		},

		"is service account on pod does not match token, expect error": {
			noDefaultTokenAudience: true,
			sentryAudience:         "spiffe://cluster.local/ns/dapr-test/dapr-sentry",
			reactor: func(t *testing.T) core.ReactionFunc {
				return func(action core.Action) (bool, runtime.Object, error) {
					obj := action.(core.CreateAction).GetObject().(*kauthapi.TokenReview)
					assert.Equal(t, []string{"dapr.io/sentry", "spiffe://cluster.local/ns/dapr-test/dapr-sentry"}, obj.Spec.Audiences)
					return true, &kauthapi.TokenReview{Status: kauthapi.TokenReviewStatus{
						Authenticated: true,
						User: kauthapi.UserInfo{
							Username: "system:serviceaccount:my-ns:my-sa",
						},
					}}, nil
				}
			},
			req: &sentryv1pb.SignCertificateRequest{
				CertificateSigningRequest: []byte("csr"),
				Namespace:                 "my-ns",
				Token:                     newToken("my-pod"),
				TrustDomain:               "example.test.dapr.io",
				Id:                        "my-app-id",
			},
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-pod",
					Namespace: "my-ns",
					Annotations: map[string]string{
						"dapr.io/app-id": "my-app-id",
						"dapr.io/config": "my-config",
					},
				},
				Spec: corev1.PodSpec{ServiceAccountName: "not-my-sa"},
			},
			config: &configapi.Configuration{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-config",
					Namespace: "my-ns",
				},
				Spec: configapi.ConfigurationSpec{
					AccessControlSpec: configapi.AccessControlSpec{
						TrustDomain: "",
					},
				},
			},
			expErr: true,
			expTD:  spiffeid.TrustDomain{},
		},

		"if username namespace doesn't match request namespace, reject": {
			noDefaultTokenAudience: true,
			sentryAudience:         "spiffe://cluster.local/ns/dapr-test/dapr-sentry",
			reactor: func(t *testing.T) core.ReactionFunc {
				return func(action core.Action) (bool, runtime.Object, error) {
					obj := action.(core.CreateAction).GetObject().(*kauthapi.TokenReview)
					assert.Equal(t, []string{"dapr.io/sentry", "spiffe://cluster.local/ns/dapr-test/dapr-sentry"}, obj.Spec.Audiences)
					return true, &kauthapi.TokenReview{Status: kauthapi.TokenReviewStatus{
						Authenticated: true,
						User: kauthapi.UserInfo{
							Username: "system:serviceaccount:my-ns:my-sa",
						},
					}}, nil
				}
			},
			req: &sentryv1pb.SignCertificateRequest{
				CertificateSigningRequest: []byte("csr"),
				Namespace:                 "not-my-ns",
				Token:                     newToken("my-pod"),
				TrustDomain:               "example.test.dapr.io",
				Id:                        "my-app-id",
			},
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-pod",
					Namespace: "my-ns",
					Annotations: map[string]string{
						"dapr.io/app-id": "my-app-id",
					},
				},
				Spec: corev1.PodSpec{ServiceAccountName: "my-sa"},
			},
			expErr: true,
			expTD:  spiffeid.TrustDomain{},
		},

		"if token returns an error, expect error": {
			noDefaultTokenAudience: true,
			sentryAudience:         "spiffe://cluster.local/ns/dapr-test/dapr-sentry",
			reactor: func(t *testing.T) core.ReactionFunc {
				var i int
				t.Cleanup(func() {
					assert.Equal(t, 2, i)
				})
				return func(action core.Action) (bool, runtime.Object, error) {
					obj := action.(core.CreateAction).GetObject().(*kauthapi.TokenReview)
					if i == 0 {
						assert.Equal(t, []string{"dapr.io/sentry", "spiffe://cluster.local/ns/dapr-test/dapr-sentry"}, obj.Spec.Audiences)
					} else {
						assert.Equal(t, []string(nil), obj.Spec.Audiences)
					}
					i++
					return true, &kauthapi.TokenReview{Status: kauthapi.TokenReviewStatus{
						Authenticated: true,
						Error:         "this is an error",
						User: kauthapi.UserInfo{
							Username: "system:serviceaccount:my-ns:my-sa",
						},
					}}, nil
				}
			},
			req: &sentryv1pb.SignCertificateRequest{
				CertificateSigningRequest: []byte("csr"),
				Namespace:                 "my-ns",
				Token:                     newToken("my-pod"),
				TrustDomain:               "example.test.dapr.io",
				Id:                        "my-app-id",
			},
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-pod",
					Namespace: "my-ns",
					Annotations: map[string]string{
						"dapr.io/app-id": "my-app-id",
					},
				},
				Spec: corev1.PodSpec{ServiceAccountName: "my-sa"},
			},
			expErr: true,
			expTD:  spiffeid.TrustDomain{},
		},

		"if noDefaultTokenAudience disabled, first token auth failed, expect a second": {
			noDefaultTokenAudience: false,
			sentryAudience:         "spiffe://cluster.local/ns/dapr-test/dapr-sentry",
			reactor: func(t *testing.T) core.ReactionFunc {
				var i int
				t.Cleanup(func() {
					assert.Equal(t, 2, i)
				})
				return func(action core.Action) (bool, runtime.Object, error) {
					i++
					obj := action.(core.CreateAction).GetObject().(*kauthapi.TokenReview)
					if i == 1 {
						assert.Equal(t, []string{"dapr.io/sentry", "spiffe://cluster.local/ns/dapr-test/dapr-sentry"}, obj.Spec.Audiences)
						return true, &kauthapi.TokenReview{Status: kauthapi.TokenReviewStatus{
							Authenticated: false,
						}}, nil
					}
					assert.Equal(t, []string(nil), obj.Spec.Audiences)
					return true, &kauthapi.TokenReview{Status: kauthapi.TokenReviewStatus{
						Authenticated: true,
						User: kauthapi.UserInfo{
							Username: "system:serviceaccount:my-ns:my-sa",
						},
					}}, nil
				}
			},
			req: &sentryv1pb.SignCertificateRequest{
				CertificateSigningRequest: []byte("csr"),
				Namespace:                 "my-ns",
				Token:                     newToken("my-pod"),
				TrustDomain:               "example.test.dapr.io",
				Id:                        "my-app-id",
			},
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-pod",
					Namespace: "my-ns",
					Annotations: map[string]string{
						"dapr.io/app-id": "my-app-id",
					},
				},
				Spec: corev1.PodSpec{ServiceAccountName: "my-sa"},
			},
			expErr: false,
			expTD:  spiffeid.RequireTrustDomainFromString("public"),
		},

		"if noDefaultTokenAudience disabled, both token auth fail, expect error": {
			noDefaultTokenAudience: false,
			sentryAudience:         "spiffe://cluster.local/ns/dapr-test/dapr-sentry",
			reactor: func(t *testing.T) core.ReactionFunc {
				var i int
				t.Cleanup(func() {
					assert.Equal(t, 2, i)
				})
				return func(action core.Action) (bool, runtime.Object, error) {
					i++
					obj := action.(core.CreateAction).GetObject().(*kauthapi.TokenReview)
					if i == 1 {
						assert.Equal(t, []string{"dapr.io/sentry", "spiffe://cluster.local/ns/dapr-test/dapr-sentry"}, obj.Spec.Audiences)
						return true, &kauthapi.TokenReview{Status: kauthapi.TokenReviewStatus{
							Authenticated: false,
						}}, nil
					}
					var empty []string
					assert.Equal(t, empty, obj.Spec.Audiences)
					return true, &kauthapi.TokenReview{Status: kauthapi.TokenReviewStatus{
						Authenticated: false,
					}}, nil
				}
			},
			req: &sentryv1pb.SignCertificateRequest{
				CertificateSigningRequest: []byte("csr"),
				Namespace:                 "my-ns",
				Token:                     newToken("my-pod"),
				TrustDomain:               "example.test.dapr.io",
				Id:                        "my-app-id",
			},
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-pod",
					Namespace: "my-ns",
					Annotations: map[string]string{
						"dapr.io/app-id": "my-app-id",
					},
				},
				Spec: corev1.PodSpec{ServiceAccountName: "my-sa"},
			},
			expErr: true,
			expTD:  spiffeid.TrustDomain{},
		},

		"if pod name is empty in kube token, expect error": {
			noDefaultTokenAudience: true,
			sentryAudience:         "spiffe://cluster.local/ns/dapr-test/dapr-sentry",
			reactor: func(t *testing.T) core.ReactionFunc {
				return func(action core.Action) (bool, runtime.Object, error) {
					obj := action.(core.CreateAction).GetObject().(*kauthapi.TokenReview)
					assert.Equal(t, []string{"dapr.io/sentry", "spiffe://cluster.local/ns/dapr-test/dapr-sentry"}, obj.Spec.Audiences)
					return true, &kauthapi.TokenReview{Status: kauthapi.TokenReviewStatus{
						Authenticated: true,
						User: kauthapi.UserInfo{
							Username: "system:serviceaccount:my-ns:my-sa",
						},
					}}, nil
				}
			},
			req: &sentryv1pb.SignCertificateRequest{
				CertificateSigningRequest: []byte("csr"),
				Namespace:                 "my-ns",
				Token:                     newToken(""),
				TrustDomain:               "example.test.dapr.io",
				Id:                        "my-app-id",
			},
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-pod",
					Namespace: "my-ns",
					Annotations: map[string]string{
						"dapr.io/app-id": "my-app-id",
					},
				},
				Spec: corev1.PodSpec{ServiceAccountName: "my-sa"},
			},
			expErr: true,
			expTD:  spiffeid.TrustDomain{},
		},
		"valid authentication with no default audience, no config annotation should be default trust domain": {
			noDefaultTokenAudience: true,
			sentryAudience:         "spiffe://cluster.local/ns/dapr-test/dapr-sentry",
			reactor: func(t *testing.T) core.ReactionFunc {
				return func(action core.Action) (bool, runtime.Object, error) {
					obj := action.(core.CreateAction).GetObject().(*kauthapi.TokenReview)
					assert.Equal(t, []string{"dapr.io/sentry", "spiffe://cluster.local/ns/dapr-test/dapr-sentry"}, obj.Spec.Audiences)
					return true, &kauthapi.TokenReview{Status: kauthapi.TokenReviewStatus{
						Authenticated: true,
						User: kauthapi.UserInfo{
							Username: "system:serviceaccount:my-ns:my-sa",
						},
					}}, nil
				}
			},
			req: &sentryv1pb.SignCertificateRequest{
				CertificateSigningRequest: []byte("csr"),
				Namespace:                 "my-ns",
				Token:                     newToken("my-pod"),
				TrustDomain:               "example.test.dapr.io",
				Id:                        "my-app-id",
			},
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-pod",
					Namespace: "my-ns",
					Annotations: map[string]string{
						"dapr.io/app-id": "my-app-id",
					},
				},
				Spec: corev1.PodSpec{ServiceAccountName: "my-sa"},
			},
			expErr: false,
			expTD:  spiffeid.RequireTrustDomainFromString("public"),
		},
		"valid authentication with no default audience, config annotation, return the trust domain from config": {
			noDefaultTokenAudience: true,
			sentryAudience:         "spiffe://cluster.local/ns/dapr-test/dapr-sentry",
			reactor: func(t *testing.T) core.ReactionFunc {
				return func(action core.Action) (bool, runtime.Object, error) {
					obj := action.(core.CreateAction).GetObject().(*kauthapi.TokenReview)
					assert.Equal(t, []string{"dapr.io/sentry", "spiffe://cluster.local/ns/dapr-test/dapr-sentry"}, obj.Spec.Audiences)
					return true, &kauthapi.TokenReview{Status: kauthapi.TokenReviewStatus{
						Authenticated: true,
						User: kauthapi.UserInfo{
							Username: "system:serviceaccount:my-ns:my-sa",
						},
					}}, nil
				}
			},
			req: &sentryv1pb.SignCertificateRequest{
				CertificateSigningRequest: []byte("csr"),
				Namespace:                 "my-ns",
				Token:                     newToken("my-pod"),
				TrustDomain:               "example.test.dapr.io",
				Id:                        "my-app-id",
			},
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-pod",
					Namespace: "my-ns",
					Annotations: map[string]string{
						"dapr.io/app-id": "my-app-id",
						"dapr.io/config": "my-config",
					},
				},
				Spec: corev1.PodSpec{ServiceAccountName: "my-sa"},
			},
			config: &configapi.Configuration{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-config",
					Namespace: "my-ns",
				},
				Spec: configapi.ConfigurationSpec{
					AccessControlSpec: configapi.AccessControlSpec{
						TrustDomain: "example.test.dapr.io",
					},
				},
			},
			expErr: false,
			expTD:  spiffeid.RequireTrustDomainFromString("example.test.dapr.io"),
		},
		"valid authentication with no default audience, config annotation, config empty, return the default trust domain": {
			noDefaultTokenAudience: true,
			sentryAudience:         "spiffe://cluster.local/ns/dapr-test/dapr-sentry",
			reactor: func(t *testing.T) core.ReactionFunc {
				return func(action core.Action) (bool, runtime.Object, error) {
					obj := action.(core.CreateAction).GetObject().(*kauthapi.TokenReview)
					assert.Equal(t, []string{"dapr.io/sentry", "spiffe://cluster.local/ns/dapr-test/dapr-sentry"}, obj.Spec.Audiences)
					return true, &kauthapi.TokenReview{Status: kauthapi.TokenReviewStatus{
						Authenticated: true,
						User: kauthapi.UserInfo{
							Username: "system:serviceaccount:my-ns:my-sa",
						},
					}}, nil
				}
			},
			req: &sentryv1pb.SignCertificateRequest{
				CertificateSigningRequest: []byte("csr"),
				Namespace:                 "my-ns",
				Token:                     newToken("my-pod"),
				TrustDomain:               "example.test.dapr.io",
				Id:                        "my-app-id",
			},
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-pod",
					Namespace: "my-ns",
					Annotations: map[string]string{
						"dapr.io/app-id": "my-app-id",
						"dapr.io/config": "my-config",
					},
				},
				Spec: corev1.PodSpec{ServiceAccountName: "my-sa"},
			},
			config: &configapi.Configuration{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-config",
					Namespace: "my-ns",
				},
				Spec: configapi.ConfigurationSpec{
					AccessControlSpec: configapi.AccessControlSpec{
						TrustDomain: "",
					},
				},
			},
			expErr: false,
			expTD:  spiffeid.RequireTrustDomainFromString("public"),
		},
		"valid authentication with no default audience, if sentry return trust domain of control-plane": {
			noDefaultTokenAudience: true,
			sentryAudience:         "spiffe://cluster.local/ns/dapr-test/dapr-sentry",
			reactor: func(t *testing.T) core.ReactionFunc {
				return func(action core.Action) (bool, runtime.Object, error) {
					obj := action.(core.CreateAction).GetObject().(*kauthapi.TokenReview)
					assert.Equal(t, []string{"dapr.io/sentry", "spiffe://cluster.local/ns/dapr-test/dapr-sentry"}, obj.Spec.Audiences)
					return true, &kauthapi.TokenReview{Status: kauthapi.TokenReviewStatus{
						Authenticated: true,
						User: kauthapi.UserInfo{
							Username: "system:serviceaccount:dapr-test:dapr-sentry",
						},
					}}, nil
				}
			},
			req: &sentryv1pb.SignCertificateRequest{
				CertificateSigningRequest: []byte("csr"),
				Namespace:                 "dapr-test",
				Token:                     newToken("dapr-sentry"),
				TrustDomain:               "example.test.dapr.io",
				Id:                        "dapr-sentry",
			},
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "dapr-sentry",
					Namespace: "dapr-test",
				},
				Spec: corev1.PodSpec{ServiceAccountName: "dapr-sentry"},
			},
			expErr: false,
			expTD:  spiffeid.RequireTrustDomainFromString("cluster.local"),
		},
		"valid authentication with no default audience, if operator return trust domain of control-plane": {
			noDefaultTokenAudience: true,
			sentryAudience:         "spiffe://cluster.local/ns/dapr-test/dapr-sentry",
			reactor: func(t *testing.T) core.ReactionFunc {
				return func(action core.Action) (bool, runtime.Object, error) {
					obj := action.(core.CreateAction).GetObject().(*kauthapi.TokenReview)
					assert.Equal(t, []string{"dapr.io/sentry", "spiffe://cluster.local/ns/dapr-test/dapr-sentry"}, obj.Spec.Audiences)
					return true, &kauthapi.TokenReview{Status: kauthapi.TokenReviewStatus{
						Authenticated: true,
						User: kauthapi.UserInfo{
							Username: "system:serviceaccount:dapr-test:dapr-operator",
						},
					}}, nil
				}
			},
			req: &sentryv1pb.SignCertificateRequest{
				CertificateSigningRequest: []byte("csr"),
				Namespace:                 "dapr-test",
				Token:                     newToken("dapr-operator"),
				TrustDomain:               "example.test.dapr.io",
				Id:                        "dapr-operator",
			},
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "dapr-operator",
					Namespace: "dapr-test",
				},
				Spec: corev1.PodSpec{ServiceAccountName: "dapr-operator"},
			},
			expErr: false,
			expTD:  spiffeid.RequireTrustDomainFromString("cluster.local"),
		},
		"valid authentication with no default audience, if injector return trust domain of control-plane": {
			noDefaultTokenAudience: true,
			sentryAudience:         "spiffe://cluster.local/ns/dapr-test/dapr-sentry",
			reactor: func(t *testing.T) core.ReactionFunc {
				return func(action core.Action) (bool, runtime.Object, error) {
					obj := action.(core.CreateAction).GetObject().(*kauthapi.TokenReview)
					assert.Equal(t, []string{"dapr.io/sentry", "spiffe://cluster.local/ns/dapr-test/dapr-sentry"}, obj.Spec.Audiences)
					return true, &kauthapi.TokenReview{Status: kauthapi.TokenReviewStatus{
						Authenticated: true,
						User: kauthapi.UserInfo{
							Username: "system:serviceaccount:dapr-test:dapr-injector",
						},
					}}, nil
				}
			},
			req: &sentryv1pb.SignCertificateRequest{
				CertificateSigningRequest: []byte("csr"),
				Namespace:                 "dapr-test",
				Token:                     newToken("dapr-injector"),
				TrustDomain:               "example.test.dapr.io",
				Id:                        "dapr-injector",
			},
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "dapr-injector",
					Namespace: "dapr-test",
				},
				Spec: corev1.PodSpec{ServiceAccountName: "dapr-injector"},
			},
			expErr: false,
			expTD:  spiffeid.RequireTrustDomainFromString("cluster.local"),
		},
		"valid authentication with no default audience, if placement return trust domain of control-plane": {
			noDefaultTokenAudience: true,
			sentryAudience:         "spiffe://cluster.local/ns/dapr-test/dapr-sentry",
			reactor: func(t *testing.T) core.ReactionFunc {
				return func(action core.Action) (bool, runtime.Object, error) {
					obj := action.(core.CreateAction).GetObject().(*kauthapi.TokenReview)
					assert.Equal(t, []string{"dapr.io/sentry", "spiffe://cluster.local/ns/dapr-test/dapr-sentry"}, obj.Spec.Audiences)
					return true, &kauthapi.TokenReview{Status: kauthapi.TokenReviewStatus{
						Authenticated: true,
						User: kauthapi.UserInfo{
							Username: "system:serviceaccount:dapr-test:dapr-placement",
						},
					}}, nil
				}
			},
			req: &sentryv1pb.SignCertificateRequest{
				CertificateSigningRequest: []byte("csr"),
				Namespace:                 "dapr-test",
				Token:                     newToken("dapr-placement"),
				TrustDomain:               "example.test.dapr.io",
				Id:                        "dapr-placement",
			},
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "dapr-placement",
					Namespace: "dapr-test",
				},
				Spec: corev1.PodSpec{ServiceAccountName: "dapr-placement"},
			},
			expErr: false,
			expTD:  spiffeid.RequireTrustDomainFromString("cluster.local"),
		},
		"valid authentication with no default audience, config annotation, using legacy request ID, return the trust domain from config": {
			noDefaultTokenAudience: true,
			sentryAudience:         "spiffe://cluster.local/ns/dapr-test/dapr-sentry",
			reactor: func(t *testing.T) core.ReactionFunc {
				return func(action core.Action) (bool, runtime.Object, error) {
					obj := action.(core.CreateAction).GetObject().(*kauthapi.TokenReview)
					assert.Equal(t, []string{"dapr.io/sentry", "spiffe://cluster.local/ns/dapr-test/dapr-sentry"}, obj.Spec.Audiences)
					return true, &kauthapi.TokenReview{Status: kauthapi.TokenReviewStatus{
						Authenticated: true,
						User: kauthapi.UserInfo{
							Username: "system:serviceaccount:my-ns:my-sa",
						},
					}}, nil
				}
			},
			req: &sentryv1pb.SignCertificateRequest{
				CertificateSigningRequest: []byte("csr"),
				Namespace:                 "my-ns",
				Token:                     newToken("my-pod"),
				TrustDomain:               "example.test.dapr.io",
				Id:                        "my-ns:my-sa",
			},
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-pod",
					Namespace: "my-ns",
					Annotations: map[string]string{
						"dapr.io/app-id": "my-app-id",
						"dapr.io/config": "my-config",
					},
				},
				Spec: corev1.PodSpec{ServiceAccountName: "my-sa"},
			},
			config: &configapi.Configuration{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-config",
					Namespace: "my-ns",
				},
				Spec: configapi.ConfigurationSpec{
					AccessControlSpec: configapi.AccessControlSpec{
						TrustDomain: "example.test.dapr.io",
					},
				},
			},
			expErr: false,
			expTD:  spiffeid.RequireTrustDomainFromString("example.test.dapr.io"),
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			var kobjs []runtime.Object
			if test.pod != nil {
				kobjs = append(kobjs, test.pod)
			}

			kubeCl := kubefake.NewSimpleClientset(kobjs...)
			kubeCl.Fake.PrependReactor("create", "tokenreviews", test.reactor(t))
			client := clientfake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(kobjs...).Build()

			if test.config != nil {
				require.NoError(t, client.Create(context.Background(), test.config))
			}

			k := &kubernetes{
				auth:           kubeCl.AuthenticationV1(),
				client:         client,
				controlPlaneNS: "dapr-test",
				controlPlaneTD: sentryID.TrustDomain(),
				sentryAudience: test.sentryAudience,
				ready:          func(_ context.Context) bool { return true },
			}

			td, err := k.Validate(context.Background(), test.req)
			assert.Equal(t, test.expErr, err != nil, "%v", err)
			assert.Equal(t, test.expTD, td)
		})
	}
}
