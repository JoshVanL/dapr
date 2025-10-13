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

	"github.com/cenkalti/backoff/v4"
	"github.com/dapr/dapr/pkg/actors/api"
	targetserrors "github.com/dapr/dapr/pkg/actors/targets/errors"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	diagutils "github.com/dapr/dapr/pkg/diagnostics/utils"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type CallReminderOptions struct {
	IsRemote      bool
	TimerCallback string
}

func (r *router) CallReminder(ctx context.Context, req *internalv1pb.Reminder, opts CallReminderOptions) error {
	if req.SkipLock {
		return r.callReminder(ctx, req, opts)
	}

	if r.resiliency.PolicyDefined(req.ActorType, resiliency.ActorPolicy{}) {
		return r.callReminder(ctx, req, opts)
	} else {
		policyRunner := resiliency.NewRunner[struct{}](ctx, r.resiliency.BuiltInPolicy(resiliency.BuiltInActorNotFoundRetries))
		_, err := policyRunner(func(ctx context.Context) (struct{}, error) {
			return struct{}{}, r.callReminder(ctx, req, opts)
		})
		return err
	}
}

func (r *router) callReminder(ctx context.Context, req *internalv1pb.Reminder, opts CallReminderOptions) error {
	if !req.SkipLock {
		var cancel context.CancelFunc
		var err error
		ctx, cancel, err = r.placement.Lock(ctx)
		if err != nil {
			return backoff.Permanent(err)
		}
		defer cancel()
	}

	lar, err := r.placement.LookupActor(ctx, &api.LookupActorRequest{
		ActorType: req.GetActorType(),
		ActorID:   req.GetActorId(),
	})
	if err != nil {
		return err
	}

	if !lar.Local {
		if opts.IsRemote {
			return backoff.Permanent(errors.New("remote actor moved"))
		}

		err = r.callRemoteActorReminder(ctx, lar, req)
		status, ok := status.FromError(err)
		if ok && status.Code() == codes.Unavailable {
			return backoff.Permanent(err)
		}
		return err
	}

	for {
		target, err := r.table.GetOrCreate(req.GetActorType(), req.GetActorId())
		if err != nil {
			return backoff.Permanent(err)
		}

		if req.IsTimer {
			err = target.InvokeTimer(ctx, req, opts.TimerCallback)
		} else {
			err = target.InvokeReminder(ctx, req)
		}

		if targetserrors.IsClosed(err) {
			continue
		}

		return backoff.Permanent(err)
	}
}

func (r *router) callRemoteActorReminder(ctx context.Context, lar *api.LookupActorResponse, reminder *internalv1pb.Reminder) error {
	conn, cancel, err := r.grpc.GetGRPCConnection(ctx, lar.Address, lar.AppID, r.namespace)
	if err != nil {
		return err
	}
	defer cancel(false)

	span := diagutils.SpanFromContext(ctx)
	ctx = diag.SpanContextToGRPCMetadata(ctx, span.SpanContext())
	client := internalv1pb.NewServiceInvocationClient(conn)

	_, err = client.CallActorReminder(ctx, reminder)
	return err
}
