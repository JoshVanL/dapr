/*
Copyright 2024 The Dapr Authors
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

package router

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/cenkalti/backoff/v4"
	"github.com/dapr/dapr/pkg/actors/api"
	actorerrors "github.com/dapr/dapr/pkg/actors/errors"
	targetserrors "github.com/dapr/dapr/pkg/actors/targets/errors"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	diagutils "github.com/dapr/dapr/pkg/diagnostics/utils"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (r *router) Call(ctx context.Context, req *internalv1pb.InternalInvokeRequest) (*internalv1pb.InternalInvokeResponse, error) {
	var res *internalv1pb.InternalInvokeResponse
	var err error

	if r.resiliency.PolicyDefined(req.GetActor().GetActorType(), resiliency.ActorPolicy{}) {
		res, err = r.callActor(ctx, req)
	} else {
		policyRunner := resiliency.NewRunner[*internalv1pb.InternalInvokeResponse](ctx, r.resiliency.BuiltInPolicy(resiliency.BuiltInActorNotFoundRetries))
		res, err = policyRunner(func(ctx context.Context) (*internalv1pb.InternalInvokeResponse, error) {
			return r.callActor(ctx, req)
		})
	}

	// Don't bubble perminant errors up to the caller to interfere with top level
	// retries.
	if _, ok := err.(*backoff.PermanentError); ok {
		err = errors.Unwrap(err)
	}

	return res, err
}

func (r *router) callActor(ctx context.Context, req *internalv1pb.InternalInvokeRequest) (*internalv1pb.InternalInvokeResponse, error) {
	// If we are in a reentrancy which is local, skip the placement lock.
	_, isDaprRemote := req.GetMetadata()["X-Dapr-Remote"]
	_, isAPICall := req.GetMetadata()["Dapr-API-Call"]

	if isAPICall || isDaprRemote {
		var cancel context.CancelFunc
		var err error
		ctx, cancel, err = r.placement.Lock(ctx)
		if err != nil {
			return nil, backoff.Permanent(err)
		}
		defer cancel()
	}

	lar, err := r.placement.LookupActor(ctx, &api.LookupActorRequest{
		ActorType: req.GetActor().GetActorType(),
		ActorID:   req.GetActor().GetActorId(),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to lookup actor: %w", err)
	}

	if lar.Local {
		for {
			var resp *internalv1pb.InternalInvokeResponse
			resp, err = r.callLocalActor(ctx, req)
			if err != nil {
				if targetserrors.IsClosed(err) {
					continue
				}
				return resp, backoff.Permanent(err)
			}
			return resp, nil
		}
	}

	// If this is a dapr-dapr call and the actor didn't pass the local check
	// above, it means it has been moved in the meantime
	if isDaprRemote {
		return nil, backoff.Permanent(errors.New("remote actor moved"))
	}

	res, err := r.callRemoteActor(ctx, lar, req)
	if err == nil {
		return res, nil
	}

	attempt := resiliency.GetAttempt(ctx)
	s, ok := status.FromError(err)
	if ok {
		if s.Code() == codes.Unavailable ||
			(s.Code() == codes.Internal &&
				(s.Message() == "error invoke actor method: remote actor moved" ||
					strings.HasSuffix(s.Message(), ": placement is disseminating"))) {
			// Destroy the connection and force a re-connection on the next attempt
			return res, fmt.Errorf("failed to invoke target %s after %d retries. Error: %w", lar.Address, attempt-1, err)
		}
	}

	return res, backoff.Permanent(err)
}

func (r *router) callLocalActor(ctx context.Context, req *internalv1pb.InternalInvokeRequest) (*internalv1pb.InternalInvokeResponse, error) {
	target, err := r.table.GetOrCreate(req.GetActor().GetActorType(), req.GetActor().GetActorId())
	if err != nil {
		return nil, err
	}

	return target.InvokeMethod(ctx, req)
}

func (r *router) callStream(ctx context.Context,
	req *internalv1pb.InternalInvokeRequest,
	stream func(*internalv1pb.InternalInvokeResponse) (bool, error),
) error {
	ctx, pcancel, err := r.placement.Lock(ctx)
	if err != nil {
		return backoff.Permanent(err)
	}
	defer pcancel()

	lar, err := r.placement.LookupActor(ctx, &api.LookupActorRequest{
		ActorType: req.GetActor().GetActorType(),
		ActorID:   req.GetActor().GetActorId(),
	})
	if err != nil {
		return err
	}

	if !lar.Local {
		// If this is a dapr-dapr call and the actor didn't pass the local check
		// above, it means it has been moved in the meantime
		if _, ok := req.GetMetadata()["X-Dapr-Remote"]; ok {
			return backoff.Permanent(errors.New("remote actor moved"))
		}

		return r.callRemoteActorStream(ctx, lar, req, stream)
	}

	return r.callLocalActorStream(ctx, req, stream)
}

func (r *router) callRemoteActor(ctx context.Context, lar *api.LookupActorResponse, req *internalv1pb.InternalInvokeRequest) (*internalv1pb.InternalInvokeResponse, error) {
	conn, cancel, err := r.grpc.GetGRPCConnection(ctx, lar.Address, lar.AppID, r.namespace)
	if err != nil {
		return nil, err
	}
	defer cancel(false)

	span := diagutils.SpanFromContext(ctx)
	ctx = diag.SpanContextToGRPCMetadata(ctx, span.SpanContext())
	client := internalv1pb.NewServiceInvocationClient(conn)

	res, err := client.CallActor(ctx, req, r.callOptions...)
	if err != nil {
		return nil, err
	}

	if len(res.GetHeaders()["X-Daprerrorresponseheader"].GetValues()) > 0 {
		return res, actorerrors.NewActorError(res)
	}

	return res, nil
}
