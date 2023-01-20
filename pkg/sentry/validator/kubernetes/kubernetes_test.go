package kubernetes

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	kauthapi "k8s.io/api/authentication/v1"
	"k8s.io/apimachinery/pkg/runtime"
	fake "k8s.io/client-go/kubernetes/fake"
	core "k8s.io/client-go/testing"
)

func TestValidate(t *testing.T) {
	tests := map[string]struct {
		reactor              core.ReactionFunc
		id, token, namespace string
		expErr               error
	}{
		"invalid token": {
			reactor: func(action core.Action) (bool, runtime.Object, error) {
				return true, &kauthapi.TokenReview{Status: kauthapi.TokenReviewStatus{Error: "bad token"}}, nil
			},
			id: "a1:ns2", token: "a2:ns2", namespace: "ns2",
			expErr: fmt.Errorf("%s: invalid token: bad token", errPrefix),
		},
		"unauthenticated": {
			reactor: func(action core.Action) (bool, runtime.Object, error) {
				return true, &kauthapi.TokenReview{Status: kauthapi.TokenReviewStatus{Authenticated: false}}, nil
			},
			id: "a1:ns2", token: "a2:ns2", namespace: "ns",
			expErr: fmt.Errorf("%s: authentication failed", errPrefix),
		},
		"bad token structure": {
			reactor: func(action core.Action) (bool, runtime.Object, error) {
				return true, &kauthapi.TokenReview{Status: kauthapi.TokenReviewStatus{Authenticated: true, User: kauthapi.UserInfo{Username: "name"}}}, nil
			},
			id: "a1:ns1", token: "a2:ns2", namespace: "ns2",
			expErr: fmt.Errorf("%s: provided token is not a properly structured service account token", errPrefix),
		},
		"empty token": {
			reactor: nil,
			id:      "a1:ns1", token: "", namespace: "ns1",
			expErr: fmt.Errorf("%s: token field in request must not be empty", errPrefix),
		},
		"empty id": {
			reactor: nil,
			id:      "", token: "a1:ns1", namespace: "ns",
			expErr: fmt.Errorf("%s: id field in request must not be empty", errPrefix),
		},
		"valid authentication": {
			reactor: func(action core.Action) (bool, runtime.Object, error) {
				return true, &kauthapi.TokenReview{Status: kauthapi.TokenReviewStatus{Authenticated: true, User: kauthapi.UserInfo{Username: "system:serviceaccount:ns1:a1"}}}, nil
			},
			id: "a1:ns1", token: "a1:ns1", namespace: "ns1",
			expErr: nil,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			fakeClient := &fake.Clientset{}
			fakeClient.Fake.AddReactor("create", "tokenreviews", test.reactor)
			k := kubernetes{
				auth: fakeClient.AuthenticationV1(),
			}
			err := k.Validate(context.TODO(), test.id, test.token, test.namespace)
			assert.Equal(t, test.expErr, err)
		})
	}
}
