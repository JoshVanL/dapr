/*
Copyright 2025 The Dapr Authors
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

package executor

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"google.golang.org/protobuf/types/known/anypb"

	actorapi "github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/table"
	"github.com/dapr/dapr/pkg/actors/targets"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.actors.targets.executor")

const (
	MethodComplete      = "Complete"
	MethodWatchComplete = "WatchComplete"
)

type Options struct {
	ActorType string
	Table     table.Interface
}

type executor struct {
	actorType string
	actorID   string
	key       string

	table table.Interface

	closeCh              chan struct{}
	completeCh           chan *internalsv1pb.InternalInvokeResponse
	completeCalled       atomic.Bool
	completeStreamCalled atomic.Bool

	completeCache *anypb.Any
	lock          chan struct{}
}

func Factory(opts Options) targets.Factory {
	return func(actorID string) targets.Interface {
		return &executor{
			actorType:  opts.ActorType,
			actorID:    actorID,
			key:        opts.ActorType + actorapi.DaprSeparator + actorID,
			table:      opts.Table,
			completeCh: make(chan *internalsv1pb.InternalInvokeResponse),
			closeCh:    make(chan struct{}),
			lock:       make(chan struct{}, 1),
		}
	}
}

var i atomic.Int64

func (e *executor) InvokeMethod(ctx context.Context, req *internalsv1pb.InternalInvokeRequest) (*internalsv1pb.InternalInvokeResponse, error) {
	ff := i.Add(1)
	fmt.Printf(">>EXECUTOR: INVOKE: INSIDE: %d\n", ff)
	defer fmt.Printf(">>EXECUTOR: INVOKE: OUTSIDE: %d\n", ff)
	switch req.GetMessage().GetMethod() {
	case MethodComplete:
		return nil, e.complete(ctx, req)
	default:
		return nil, errors.New("unknown method: " + req.GetMessage().GetMethod())
	}
}

func (e *executor) complete(ctx context.Context, req *internalsv1pb.InternalInvokeRequest) error {
	defer e.table.DeleteFromTableIn(e, time.Second*20)
	//if !e.completeCalled.CompareAndSwap(false, true) {
	//	return errors.New("complete already called")
	//}

	d := &internalsv1pb.InternalInvokeResponse{
		Message: &commonv1pb.InvokeResponse{
			Data: req.GetMessage().GetData(),
		},
	}

	fmt.Printf(">>EXECUTOR: GOT COMPLETE REQUEST: %s\n", e.actorID)
	select {
	case e.completeCh <- d:
		fmt.Printf(">>EXECUTOR: COMPLETE SENT: %s\n", e.actorID)
		//e.table.DeleteFromTableIn(e, 0)
		return nil
	case <-e.closeCh:
		fmt.Printf(">>EXECUTOR: COMPLETE CLOSED: %s\n", e.actorID)
		return errors.New("executor closed")
	case <-ctx.Done():
		fmt.Printf(">>EXECUTOR: COMPLETE CONTEXT CANCLLED: %s\n", e.actorID)
		//e.table.DeleteFromTableIn(e, 0)
		return errors.New("context cancelled before completion result was sent")
	}
}

func (e *executor) InvokeReminder(ctx context.Context, reminder *actorapi.Reminder) error {
	return errors.New("reminders are not implemented")
}

func (e *executor) InvokeTimer(ctx context.Context, reminder *actorapi.Reminder) error {
	return errors.New("timers are not implemented")
}

func (e *executor) Deactivate() error {
	close(e.closeCh)
	return nil
}

func (e *executor) InvokeStream(ctx context.Context, req *internalsv1pb.InternalInvokeRequest, ch chan<- *internalsv1pb.InternalInvokeResponse) error {
	ff := i.Add(1)
	fmt.Printf(">>EXECUTOR: STREAM: INSIDE: %d\n", ff)
	defer fmt.Printf(">>EXECUTOR: STREAM: OUTSIDE: %d\n", ff)
	switch req.GetMessage().GetMethod() {
	case MethodWatchComplete:
		return e.watchComplete(ctx, ch)
	default:
		return errors.New("unknown method: " + req.GetMessage().GetMethod())
	}
}

func (e *executor) watchComplete(ctx context.Context, ch chan<- *internalsv1pb.InternalInvokeResponse) error {
	select {
	case e.lock <- struct{}{}:
	case <-e.closeCh:
		return errors.New("executor closed")
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() {
		<-e.lock
	}()

	//if e.completeCache != nil {
	//	ch <- &internalsv1pb.InternalInvokeResponse{
	//		Message: &commonv1pb.InvokeResponse{
	//			Data: e.completeCache,
	//		},
	//	}
	//}

	fmt.Printf(">>EXECUTOR: GOT WATCH: %s\n", e.actorID)

	// TODO: @joshvanl: uncomment for fixing tests!
	//if e.completeCache != nil {
	//	fmt.Printf(">>EXECUTOR: GOT WATCH ALREADY CALLED: %s\n", e.actorID)
	//	//return errors.New("stream already called")
	//	ch <- &internalsv1pb.InternalInvokeResponse{
	//		Message: &commonv1pb.InvokeResponse{
	//			Data: e.completeCache,
	//		},
	//	}
	//	return nil
	//}

	//if e.completeCache == nil {
	select {
	case <-ctx.Done():
		fmt.Printf(">>EXECUTOR: GOT WATCH CONTEXT CANCLLED: %s\n", e.actorID)
		//e.completeStreamCalled.Store(false)
		return ctx.Err()
	case <-e.closeCh:
		return errors.New("executor closed")
	case ch <- <-e.completeCh:
		fmt.Printf(">>EXECUTOR: GOT COMPLETE SEND %s:\n", e.actorID)
		//defer e.table.DeleteFromTableIn(e, 0)
		//e.completeCache = d
		//ch <- &internalsv1pb.InternalInvokeResponse{
		//	Message: &commonv1pb.InvokeResponse{
		//		Data: d,
		//	},
		//}
		return nil
	}
	//}
}

func (e *executor) Key() string {
	return e.key
}

func (e *executor) Type() string {
	return e.actorType
}

func (e *executor) ID() string {
	return e.actorID
}
