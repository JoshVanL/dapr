package kubernetes

import (
	"context"
	"fmt"
	"strings"

	configurationapi "github.com/dapr/dapr/pkg/apis/configuration/v1alpha1"
	"github.com/dapr/kit/logger"
	"github.com/golang-jwt/jwt/v4"
	"github.com/spiffe/go-spiffe/v2/spiffeid"
	kauthapi "k8s.io/api/authentication/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8s "k8s.io/client-go/kubernetes"
	kauth "k8s.io/client-go/kubernetes/typed/authentication/v1"
	kcore "k8s.io/client-go/kubernetes/typed/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/dapr/dapr/pkg/injector/annotations"
	sentryv1pb "github.com/dapr/dapr/pkg/proto/sentry/v1"
	"github.com/dapr/dapr/pkg/sentry/validator"
)

const (
	errPrefix = "csr validation failed"

	ServiceAccountTokenAudience = "dapr.io/sentry" /* #nosec */
)

var log = logger.NewLogger("dapr.sentry.identity.kubernetes")

type kubernetes struct {
	auth                   kauth.AuthenticationV1Interface
	pod                    kcore.PodsGetter
	client                 client.Client
	audiences              []string
	noDefaultTokenAudience bool
}

type k8sClaims struct {
	Kubernetes struct {
		Pod struct {
			Name string `json:"name"`
		} `json:"pod"`
	} `json:"kubernetes.io"`
}

func (k *k8sClaims) Valid() error {
	return nil
}

func New(cclient client.Client, kclient k8s.Interface, audiences []string, noDefaultTokenAudience bool) validator.Interface {
	return &kubernetes{
		auth:      kclient.AuthenticationV1(),
		pod:       kclient.CoreV1(),
		client:    cclient,
		audiences: audiences,
		// TODO: Remove once the NoDefaultTokenAudience feature is finalized
		noDefaultTokenAudience: noDefaultTokenAudience,
	}
}

func (k *kubernetes) Validate(ctx context.Context, appID string, req *sentryv1pb.SignCertificateRequest) (spiffeid.TrustDomain, error) {
	prts, err := k.executeTokenReview(ctx, req.GetToken(), k.audiences)
	if err != nil {
		if !k.noDefaultTokenAudience {
			// Empty audience means the Kubernetes API server.
			prts, err = k.executeTokenReview(ctx, req.GetToken(), nil)
			if err != nil {
				return spiffeid.TrustDomain{}, err
			}
			log.Warn("WARNING: Sentry accepted a token with the audience for the Kubernetes API server. This is deprecated and only supported to ensure a smooth upgrade from Dapr pre-1.10.")
		} else {
			return spiffeid.TrustDomain{}, err
		}
	}

	if len(prts) != 4 || prts[0] != "system" {
		return spiffeid.TrustDomain{}, fmt.Errorf("%s: provided token is not a properly structured service account token", errPrefix)
	}

	if prts[2] != req.GetNamespace() {
		return spiffeid.TrustDomain{}, fmt.Errorf("%s: namespace mismatch. received namespace: %s",
			errPrefix, req.GetNamespace())
	}

	var claims k8sClaims
	// We have already validated to the token against Kubernetes API server.
	ptoken, _, err := jwt.NewParser().ParseUnverified(req.GetToken(), &claims)
	if err != nil {
		return spiffeid.TrustDomain{}, fmt.Errorf("%s: failed to parse kubernetes token: %s", errPrefix, err)
	}
	if err := ptoken.Claims.Valid(); err != nil {
		return spiffeid.TrustDomain{}, fmt.Errorf("%s: invalid kubernetes token: %s", errPrefix, err)
	}

	pod, err := k.pod.Pods(req.GetNamespace()).Get(ctx, claims.Kubernetes.Pod.Name, v1.GetOptions{})
	if err != nil {
		log.Error(err)
		return spiffeid.TrustDomain{}, fmt.Errorf("%s: failed to get pod of identity")
	}

	expID, ok := pod.GetAnnotations()[annotations.KeyAppID]
	if !ok {
		expID = pod.GetName()
	}

	if expID != appID {
		return spiffeid.TrustDomain{}, fmt.Errorf("%s: app-id mismatch. expected: %s, received: %s",
			errPrefix, expID, appID)
	}

	configName, ok := pod.GetAnnotations()[annotations.KeyConfig]
	if !ok {
		return spiffeid.RequireTrustDomainFromString("public"), nil
	}

	var config configurationapi.Configuration
	err = k.client.Get(ctx, client.ObjectKey{Namespace: req.GetNamespace(), Name: configName}, &config)
	if apierrors.IsNotFound(err) {
		return spiffeid.TrustDomain{}, fmt.Errorf("%s: configuration %s not found", errPrefix, configName)
	}
	if err != nil {
		log.Errorf("failed to get configuration %s: %s", configName, err)
		return spiffeid.TrustDomain{}, fmt.Errorf("%s: failed to get configuration %s")
	}

	if len(config.Spec.AccessControlSpec.TrustDomain) == 0 {
		return spiffeid.RequireTrustDomainFromString("public"), nil
	}

	return spiffeid.TrustDomainFromString(config.Spec.AccessControlSpec.TrustDomain)
}

// Executes a tokenReview, returning an error if the token is invalid or if there's a failure
func (k *kubernetes) executeTokenReview(ctx context.Context, token string, audiences []string) ([]string, error) {
	review, err := k.auth.TokenReviews().Create(ctx, &kauthapi.TokenReview{
		Spec: kauthapi.TokenReviewSpec{Token: token, Audiences: []string{ServiceAccountTokenAudience}},
	}, v1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("%s: token review failed: %w", errPrefix, err)
	}
	if review.Status.Error != "" {
		return nil, fmt.Errorf("%s: invalid token: %s", errPrefix, review.Status.Error)
	}
	if !review.Status.Authenticated {
		return nil, fmt.Errorf("%s: authentication failed", errPrefix)
	}
	return strings.Split(review.Status.User.Username, ":"), nil
}
