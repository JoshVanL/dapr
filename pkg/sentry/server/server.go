package server

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net"
	"strings"
	"sync"

	"github.com/dapr/kit/logger"
	"github.com/gogo/status"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"

	sentryv1pb "github.com/dapr/dapr/pkg/proto/sentry/v1"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/dapr/pkg/sentry/ca"
	"github.com/dapr/dapr/pkg/sentry/monitoring"
	"github.com/dapr/dapr/pkg/sentry/validator"
)

var log = logger.NewLogger("dapr.sentry.server")

// Options is the configuration for the server.
type Options struct {
	// Port is the port that the server will listen on.
	Port int

	// Security is the security listening configuration for the server.
	Security security.Interface

	// Validator is the client authentication validator.
	Validator validator.Interface

	// CA is the certificate authority which signs client certificates.
	CA ca.Interface
}

type server struct {
	val validator.Interface
	ca  ca.Interface
}

func Run(ctx context.Context, opts Options) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", opts.Port))
	if err != nil {
		return fmt.Errorf("could not listen on port %d: %s", opts.Port, err)
	}

	svr := grpc.NewServer(opts.Security.GRPCServerOptionNoClientAuth())

	s := &server{
		val: opts.Validator,
		ca:  opts.CA,
	}
	sentryv1pb.RegisterCAServer(svr, s)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-ctx.Done()
		log.Info("shutting down gRPC server")
		svr.GracefulStop()
	}()

	log.Infof("running gRPC server on port %d", opts.Port)
	err = svr.Serve(lis)
	cancel()
	wg.Wait()
	return err
}

func (s *server) SignCertificate(ctx context.Context, req *sentryv1pb.SignCertificateRequest) (*sentryv1pb.SignCertificateResponse, error) {
	monitoring.CertSignRequestReceived(ctx)
	resp, err := s.signCertificate(ctx, req)
	if err != nil {
		monitoring.CertSignFailed(ctx, "sign")
		return nil, err
	}
	monitoring.CertSignSucceed(ctx)
	return resp, nil
}

func (s *server) signCertificate(ctx context.Context, req *sentryv1pb.SignCertificateRequest) (*sentryv1pb.SignCertificateResponse, error) {
	split := strings.Split(req.GetId(), ":")
	if len(split) != 2 || len(split[0]) == 0 || len(split[1]) == 0 {
		return nil, status.Error(codes.PermissionDenied,
			"id field in request must be in the format of <namespace>:<app-id>")
	}
	if split[0] != req.GetNamespace() {
		return nil, status.Errorf(codes.PermissionDenied, "namespace mismatch in ID and requested namespace")
	}

	appID := split[1]
	trustDomain, err := s.val.Validate(ctx, appID, req)
	if err != nil {
		return nil, status.Error(codes.PermissionDenied, err.Error())
	}

	der, _ := pem.Decode(req.GetCertificateSigningRequest())
	csr, err := x509.ParseCertificateRequest(der.Bytes)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	if csr.CheckSignature() != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid signature")
	}

	var dns []string
	if req.GetNamespace() == security.CurrentNamespace() && appID == "dapr-injector" {
		dns = []string{fmt.Sprintf("dapr-sidecar-injector.%s.svc", req.GetNamespace())}
	}
	if req.GetNamespace() == security.CurrentNamespace() && appID == "dapr-operator" {
		dns = []string{fmt.Sprintf("dapr-api.%s.svc", req.GetNamespace())}
	}

	chain, err := s.ca.SignIdentity(ctx, &ca.SignRequest{
		PublicKey:          csr.PublicKey,
		SignatureAlgorithm: csr.SignatureAlgorithm,
		TrustDomain:        trustDomain.String(),
		Namespace:          req.GetNamespace(),
		AppID:              appID,
		DNS:                dns,
	})
	if err != nil {
		log.Errorf("error signing identity: %s", err)
		return nil, status.Error(codes.Internal, "failed to sign certificate")
	}

	var block []byte
	for _, cert := range chain {
		block = append(block, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})...)
		block = append(block, '\n')
	}

	return &sentryv1pb.SignCertificateResponse{
		WorkloadCertificate: block,
		// We only populate the trust chain for clients pre 1.10.
		TrustChainCertificates: [][]byte{s.ca.TrustAnchors()},
		ValidUntil:             timestamppb.New(chain[0].NotAfter),
	}, nil
}
