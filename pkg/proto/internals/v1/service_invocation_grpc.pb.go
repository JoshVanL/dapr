//
//Copyright 2021 The Dapr Authors
//Licensed under the Apache License, Version 2.0 (the "License");
//you may not use this file except in compliance with the License.
//You may obtain a copy of the License at
//http://www.apache.org/licenses/LICENSE-2.0
//Unless required by applicable law or agreed to in writing, software
//distributed under the License is distributed on an "AS IS" BASIS,
//WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
//See the License for the specific language governing permissions and
//limitations under the License.

// Code generated by protoc-gen-go-grpc. DO NOT EDIT.
// versions:
// - protoc-gen-go-grpc v1.2.0
// - protoc             v3.21.12
// source: dapr/proto/internals/v1/service_invocation.proto

package internals

import (
	context "context"
	grpc "google.golang.org/grpc"
	codes "google.golang.org/grpc/codes"
	status "google.golang.org/grpc/status"
)

// This is a compile-time assertion to ensure that this generated file
// is compatible with the grpc package it is being compiled against.
// Requires gRPC-Go v1.32.0 or later.
const _ = grpc.SupportPackageIsVersion7

// ServiceInvocationClient is the client API for ServiceInvocation service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://pkg.go.dev/google.golang.org/grpc/?tab=doc#ClientConn.NewStream.
type ServiceInvocationClient interface {
	// Invokes a method of the specific actor.
	CallActor(ctx context.Context, in *InternalInvokeRequest, opts ...grpc.CallOption) (*InternalInvokeResponse, error)
	// Invokes a method of the specific service.
	CallLocal(ctx context.Context, in *InternalInvokeRequest, opts ...grpc.CallOption) (*InternalInvokeResponse, error)
	// Invokes a method of the specific service using a stream of data.
	// Although this uses a bi-directional stream, it behaves as a "simple RPC" in which the caller sends the full request (chunked in multiple messages in the stream), then reads the full response (chunked in the stream).
	// Each message in the stream contains a `InternalInvokeRequestStream` (for caller) or `InternalInvokeResponseStream` (for callee):
	// - The first message in the stream MUST contain a `request` (caller) or `response` (callee) message with all required properties present.
	// - The first message in the stream MAY contain a `payload`, which is not required and may be empty.
	// - Subsequent messages (any message except the first one in the stream) MUST contain a `payload` and MUST NOT contain any other property (like `request` or `response`).
	// - Each message with a `payload` MUST contain a sequence number in `seq`, which is a counter that starts from 0 and MUST be incremented by 1 in each chunk. The `seq` counter MUST NOT be included if the message does not have a `payload`.
	// - When the sender has completed sending the data, it MUST call `CloseSend` on the stream.
	// The caller and callee must send at least one message in the stream. If only 1 message is sent in each direction, that message must contain both a `request`/`response` (the `payload` may be empty).
	CallLocalStream(ctx context.Context, opts ...grpc.CallOption) (ServiceInvocation_CallLocalStreamClient, error)
}

type serviceInvocationClient struct {
	cc grpc.ClientConnInterface
}

func NewServiceInvocationClient(cc grpc.ClientConnInterface) ServiceInvocationClient {
	return &serviceInvocationClient{cc}
}

func (c *serviceInvocationClient) CallActor(ctx context.Context, in *InternalInvokeRequest, opts ...grpc.CallOption) (*InternalInvokeResponse, error) {
	out := new(InternalInvokeResponse)
	err := c.cc.Invoke(ctx, "/dapr.proto.internals.v1.ServiceInvocation/CallActor", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *serviceInvocationClient) CallLocal(ctx context.Context, in *InternalInvokeRequest, opts ...grpc.CallOption) (*InternalInvokeResponse, error) {
	out := new(InternalInvokeResponse)
	err := c.cc.Invoke(ctx, "/dapr.proto.internals.v1.ServiceInvocation/CallLocal", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *serviceInvocationClient) CallLocalStream(ctx context.Context, opts ...grpc.CallOption) (ServiceInvocation_CallLocalStreamClient, error) {
	stream, err := c.cc.NewStream(ctx, &ServiceInvocation_ServiceDesc.Streams[0], "/dapr.proto.internals.v1.ServiceInvocation/CallLocalStream", opts...)
	if err != nil {
		return nil, err
	}
	x := &serviceInvocationCallLocalStreamClient{stream}
	return x, nil
}

type ServiceInvocation_CallLocalStreamClient interface {
	Send(*InternalInvokeRequestStream) error
	Recv() (*InternalInvokeResponseStream, error)
	grpc.ClientStream
}

type serviceInvocationCallLocalStreamClient struct {
	grpc.ClientStream
}

func (x *serviceInvocationCallLocalStreamClient) Send(m *InternalInvokeRequestStream) error {
	return x.ClientStream.SendMsg(m)
}

func (x *serviceInvocationCallLocalStreamClient) Recv() (*InternalInvokeResponseStream, error) {
	m := new(InternalInvokeResponseStream)
	if err := x.ClientStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

// ServiceInvocationServer is the server API for ServiceInvocation service.
// All implementations should embed UnimplementedServiceInvocationServer
// for forward compatibility
type ServiceInvocationServer interface {
	// Invokes a method of the specific actor.
	CallActor(context.Context, *InternalInvokeRequest) (*InternalInvokeResponse, error)
	// Invokes a method of the specific service.
	CallLocal(context.Context, *InternalInvokeRequest) (*InternalInvokeResponse, error)
	// Invokes a method of the specific service using a stream of data.
	// Although this uses a bi-directional stream, it behaves as a "simple RPC" in which the caller sends the full request (chunked in multiple messages in the stream), then reads the full response (chunked in the stream).
	// Each message in the stream contains a `InternalInvokeRequestStream` (for caller) or `InternalInvokeResponseStream` (for callee):
	// - The first message in the stream MUST contain a `request` (caller) or `response` (callee) message with all required properties present.
	// - The first message in the stream MAY contain a `payload`, which is not required and may be empty.
	// - Subsequent messages (any message except the first one in the stream) MUST contain a `payload` and MUST NOT contain any other property (like `request` or `response`).
	// - Each message with a `payload` MUST contain a sequence number in `seq`, which is a counter that starts from 0 and MUST be incremented by 1 in each chunk. The `seq` counter MUST NOT be included if the message does not have a `payload`.
	// - When the sender has completed sending the data, it MUST call `CloseSend` on the stream.
	// The caller and callee must send at least one message in the stream. If only 1 message is sent in each direction, that message must contain both a `request`/`response` (the `payload` may be empty).
	CallLocalStream(ServiceInvocation_CallLocalStreamServer) error
}

// UnimplementedServiceInvocationServer should be embedded to have forward compatible implementations.
type UnimplementedServiceInvocationServer struct {
}

func (UnimplementedServiceInvocationServer) CallActor(context.Context, *InternalInvokeRequest) (*InternalInvokeResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method CallActor not implemented")
}
func (UnimplementedServiceInvocationServer) CallLocal(context.Context, *InternalInvokeRequest) (*InternalInvokeResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method CallLocal not implemented")
}
func (UnimplementedServiceInvocationServer) CallLocalStream(ServiceInvocation_CallLocalStreamServer) error {
	return status.Errorf(codes.Unimplemented, "method CallLocalStream not implemented")
}

// UnsafeServiceInvocationServer may be embedded to opt out of forward compatibility for this service.
// Use of this interface is not recommended, as added methods to ServiceInvocationServer will
// result in compilation errors.
type UnsafeServiceInvocationServer interface {
	mustEmbedUnimplementedServiceInvocationServer()
}

func RegisterServiceInvocationServer(s grpc.ServiceRegistrar, srv ServiceInvocationServer) {
	s.RegisterService(&ServiceInvocation_ServiceDesc, srv)
}

func _ServiceInvocation_CallActor_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(InternalInvokeRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ServiceInvocationServer).CallActor(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/dapr.proto.internals.v1.ServiceInvocation/CallActor",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ServiceInvocationServer).CallActor(ctx, req.(*InternalInvokeRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _ServiceInvocation_CallLocal_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(InternalInvokeRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ServiceInvocationServer).CallLocal(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/dapr.proto.internals.v1.ServiceInvocation/CallLocal",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ServiceInvocationServer).CallLocal(ctx, req.(*InternalInvokeRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _ServiceInvocation_CallLocalStream_Handler(srv interface{}, stream grpc.ServerStream) error {
	return srv.(ServiceInvocationServer).CallLocalStream(&serviceInvocationCallLocalStreamServer{stream})
}

type ServiceInvocation_CallLocalStreamServer interface {
	Send(*InternalInvokeResponseStream) error
	Recv() (*InternalInvokeRequestStream, error)
	grpc.ServerStream
}

type serviceInvocationCallLocalStreamServer struct {
	grpc.ServerStream
}

func (x *serviceInvocationCallLocalStreamServer) Send(m *InternalInvokeResponseStream) error {
	return x.ServerStream.SendMsg(m)
}

func (x *serviceInvocationCallLocalStreamServer) Recv() (*InternalInvokeRequestStream, error) {
	m := new(InternalInvokeRequestStream)
	if err := x.ServerStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

// ServiceInvocation_ServiceDesc is the grpc.ServiceDesc for ServiceInvocation service.
// It's only intended for direct use with grpc.RegisterService,
// and not to be introspected or modified (even as a copy)
var ServiceInvocation_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "dapr.proto.internals.v1.ServiceInvocation",
	HandlerType: (*ServiceInvocationServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "CallActor",
			Handler:    _ServiceInvocation_CallActor_Handler,
		},
		{
			MethodName: "CallLocal",
			Handler:    _ServiceInvocation_CallLocal_Handler,
		},
	},
	Streams: []grpc.StreamDesc{
		{
			StreamName:    "CallLocalStream",
			Handler:       _ServiceInvocation_CallLocalStream_Handler,
			ServerStreams: true,
			ClientStreams: true,
		},
	},
	Metadata: "dapr/proto/internals/v1/service_invocation.proto",
}
