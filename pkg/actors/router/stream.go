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
	"io"

	"github.com/dapr/dapr/pkg/actors/api"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	diagutils "github.com/dapr/dapr/pkg/diagnostics/utils"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/pkg/resiliency"
)

func (r *router) CallStream(ctx context.Context,
	req *internalv1pb.InternalInvokeRequest,
	stream func(*internalv1pb.InternalInvokeResponse) (bool, error),
) error {
	policyRunner := resiliency.NewRunner[struct{}](ctx, r.resiliency.BuiltInPolicy(resiliency.BuiltInActorNotFoundRetries))
	_, err := policyRunner(func(ctx context.Context) (struct{}, error) {
		serr := r.callStream(ctx, req, stream)
		// Suppress EOF errors as this simply means the stream is closing.
		if errors.Is(serr, io.EOF) {
			return struct{}{}, nil
		}
		return struct{}{}, serr
	})

	return err
}
func (r *router) callLocalActorStream(ctx context.Context,
	req *internalv1pb.InternalInvokeRequest,
	stream func(*internalv1pb.InternalInvokeResponse) (bool, error),
) error {
	target, err := r.table.GetOrCreate(req.GetActor().GetActorType(), req.GetActor().GetActorId())
	if err != nil {
		return err
	}

	return target.InvokeStream(ctx, req, stream)
}

func (r *router) callRemoteActorStream(ctx context.Context,
	lar *api.LookupActorResponse,
	req *internalv1pb.InternalInvokeRequest,
	stream func(*internalv1pb.InternalInvokeResponse) (bool, error),
) error {
	conn, cancel, err := r.grpc.GetGRPCConnection(ctx, lar.Address, lar.AppID, r.namespace)
	if err != nil {
		return err
	}
	defer cancel(false)

	span := diagutils.SpanFromContext(ctx)
	ctx = diag.SpanContextToGRPCMetadata(ctx, span.SpanContext())
	client := internalv1pb.NewServiceInvocationClient(conn)

	rstream, err := client.CallActorStream(ctx, req)
	if err != nil {
		return err
	}

	for {
		resp, err := rstream.Recv()
		if err != nil {
			return err
		}

		if ok, err := stream(resp); err != nil || ok {
			return err
		}
	}
}
