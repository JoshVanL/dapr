package selfhosted

import (
	"context"

	sentryv1pb "github.com/dapr/dapr/pkg/proto/sentry/v1"
	"github.com/dapr/dapr/pkg/sentry/validator"
	"github.com/spiffe/go-spiffe/v2/spiffeid"
)

type selfhosted struct{}

func New() validator.Interface {
	return &selfhosted{}
}

func (s *selfhosted) Validate(_ context.Context, _ string, req *sentryv1pb.SignCertificateRequest) (spiffeid.TrustDomain, error) {
	// no validation for self hosted.
	if len(req.GetTrustDomain()) == 0 {
		return spiffeid.RequireTrustDomainFromString("public"), nil
	}
	return spiffeid.TrustDomainFromString(req.GetTrustDomain())
}
