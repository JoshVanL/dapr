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

	"google.golang.org/grpc"
	"k8s.io/utils/clock"

	"github.com/dapr/dapr/pkg/actors/internal/placement"
	"github.com/dapr/dapr/pkg/actors/reminders"
	"github.com/dapr/dapr/pkg/actors/table"
	"github.com/dapr/dapr/pkg/api/grpc/manager"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/pkg/resiliency"
)

type Interface interface {
	Call(ctx context.Context, req *internalv1pb.InternalInvokeRequest) (*internalv1pb.InternalInvokeResponse, error)
	CallReminder(ctx context.Context, reminder *internalv1pb.Reminder, opts CallReminderOptions) error
	CallStream(ctx context.Context, req *internalv1pb.InternalInvokeRequest, fn func(*internalv1pb.InternalInvokeResponse) (bool, error)) error
}

type Options struct {
	Namespace          string
	Table              table.Interface
	Placement          placement.Interface
	Resiliency         resiliency.Provider
	Reminders          reminders.Interface
	GRPC               *manager.Manager
	MaxRequestBodySize int
}

type router struct {
	namespace string

	table      table.Interface
	placement  placement.Interface
	resiliency resiliency.Provider
	reminders  reminders.Interface
	grpc       *manager.Manager

	clock clock.Clock

	callOptions []grpc.CallOption
}

func New(opts Options) Interface {
	return &router{
		namespace:  opts.Namespace,
		table:      opts.Table,
		placement:  opts.Placement,
		resiliency: opts.Resiliency,
		grpc:       opts.GRPC,
		reminders:  opts.Reminders,
		clock:      clock.RealClock{},
		callOptions: []grpc.CallOption{
			grpc.MaxCallRecvMsgSize(opts.MaxRequestBodySize),
			grpc.MaxCallSendMsgSize(opts.MaxRequestBodySize),
		},
	}
}
