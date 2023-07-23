/*
Copyright 2022 The Dapr Authors
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

package universal

import (
	"context"

	"github.com/dapr/components-contrib/lock"
	lockLoader "github.com/dapr/dapr/pkg/components/lock"
	"github.com/dapr/dapr/pkg/messages"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/resiliency"
)

func (u *Universal) TryLockAlpha1(ctx context.Context, req *runtimev1pb.TryLockRequest) (*runtimev1pb.TryLockResponse, error) {
	// 1. validate and find lock component
	if req.ExpiryInSeconds <= 0 {
		err := messages.ErrExpiryInSecondsNotPositive.WithFormat(req.StoreName)
		u.logger.Debug(err)
		return &runtimev1pb.TryLockResponse{}, err
	}
	store, err := u.lockValidateRequest(req)
	if err != nil {
		return &runtimev1pb.TryLockResponse{}, err
	}

	// 2. convert request
	compReq := &lock.TryLockRequest{
		ResourceID:      req.ResourceId,
		LockOwner:       req.LockOwner,
		ExpiryInSeconds: req.ExpiryInSeconds,
	}
	// modify key
	compReq.ResourceID, err = lockLoader.GetModifiedLockKey(compReq.ResourceID, req.StoreName, u.appID)
	if err != nil {
		err = messages.ErrTryLockFailed.WithFormat(err)
		u.logger.Debug(err)
		return &runtimev1pb.TryLockResponse{}, err
	}

	// 3. delegate to the component
	policyRunner := resiliency.NewRunner[*lock.TryLockResponse](ctx,
		u.resiliency.ComponentOutboundPolicy(req.StoreName, resiliency.Lock),
	)
	resp, err := policyRunner(func(ctx context.Context) (*lock.TryLockResponse, error) {
		return store.TryLock(ctx, compReq)
	})
	if err != nil {
		err = messages.ErrTryLockFailed.WithFormat(err)
		u.logger.Debug(err)
		return &runtimev1pb.TryLockResponse{}, err
	}

	// 4. convert response
	if resp == nil {
		return &runtimev1pb.TryLockResponse{}, nil
	}
	return &runtimev1pb.TryLockResponse{
		Success: resp.Success,
	}, nil
}

func (u *Universal) UnlockAlpha1(ctx context.Context, req *runtimev1pb.UnlockRequest) (*runtimev1pb.UnlockResponse, error) {
	var err error

	// 1. validate and find lock component
	store, err := u.lockValidateRequest(req)
	if err != nil {
		return newInternalErrorUnlockResponse(), err
	}

	// 2. convert request
	compReq := &lock.UnlockRequest{
		ResourceID: req.ResourceId,
		LockOwner:  req.LockOwner,
	}
	// modify key
	compReq.ResourceID, err = lockLoader.GetModifiedLockKey(compReq.ResourceID, req.StoreName, u.appID)
	if err != nil {
		err = messages.ErrUnlockFailed.WithFormat(err)
		u.logger.Debug(err)
		return newInternalErrorUnlockResponse(), err
	}

	// 3. delegate to the component
	policyRunner := resiliency.NewRunner[*lock.UnlockResponse](ctx,
		u.resiliency.ComponentOutboundPolicy(req.StoreName, resiliency.Lock),
	)
	resp, err := policyRunner(func(ctx context.Context) (*lock.UnlockResponse, error) {
		return store.Unlock(ctx, compReq)
	})
	if err != nil {
		err = messages.ErrUnlockFailed.WithFormat(err)
		u.logger.Debug(err)
		return newInternalErrorUnlockResponse(), err
	}

	// 4. convert response
	if resp == nil {
		return &runtimev1pb.UnlockResponse{}, nil
	}
	return &runtimev1pb.UnlockResponse{
		//nolint:nosnakecase
		Status: runtimev1pb.UnlockResponse_Status(resp.Status),
	}, nil
}

// Interface for both *runtimev1pb.TryLockRequest and *runtimev1pb.UnlockRequest
type tryLockUnlockRequest interface {
	GetResourceId() string
	GetLockOwner() string
	GetStoreName() string
}

// Internal method that checks if the request is for a lock store component.
func (u *Universal) lockValidateRequest(req tryLockUnlockRequest) (lock.Store, error) {
	var err error

	if u.compStore.LocksLen() == 0 {
		err = messages.ErrLockStoresNotConfigured
		u.logger.Debug(err)
		return nil, err
	}
	if req.GetResourceId() == "" {
		err = messages.ErrResourceIDEmpty.WithFormat(req.GetStoreName())
		u.logger.Debug(err)
		return nil, err
	}
	if req.GetLockOwner() == "" {
		err = messages.ErrLockOwnerEmpty.WithFormat(req.GetStoreName())
		u.logger.Debug(err)
		return nil, err
	}

	// 2. find lock component
	store, ok := u.compStore.GetLock(req.GetStoreName())
	if !ok {
		err = messages.ErrLockStoreNotFound.WithFormat(req.GetStoreName())
		u.logger.Debug(err)
		return nil, err
	}

	return store, nil
}

func newInternalErrorUnlockResponse() *runtimev1pb.UnlockResponse {
	return &runtimev1pb.UnlockResponse{
		//nolint:nosnakecase
		Status: runtimev1pb.UnlockResponse_INTERNAL_ERROR,
	}
}
