package validator

import (
	"context"

	sentryv1pb "github.com/dapr/dapr/pkg/proto/sentry/v1"
	"github.com/spiffe/go-spiffe/v2/spiffeid"
)

// Interface is used to validate the identity of a certificate requester by
// using an ID and token.
// Returns the trust domain of the certificate requester.
type Interface interface {
	Validate(ctx context.Context, appID string, req *sentryv1pb.SignCertificateRequest) (spiffeid.TrustDomain, error)
}
