// Code generated by protoc-gen-go-grpc. DO NOT EDIT.
// versions:
// - protoc-gen-go-grpc v1.2.0
// - protoc             v3.21.12
// source: dapr/proto/scheduler/v1/scheduler.proto

package scheduler

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

// SchedulerClient is the client API for Scheduler service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://pkg.go.dev/google.golang.org/grpc/?tab=doc#ClientConn.NewStream.
type SchedulerClient interface {
	// ConnectHost is used by the daprd sidecar to connect to the scheduler service.
	// The sidecar will send a stream of RegisterRequest, UnregisterRequest and blank
	// health-check messages.
	// In return, scheduler service will send a stream of blank ConnectServerStream messages,
	// which will also act as health check messages.
	ConnectHost(ctx context.Context, in *ConnectClientStream, opts ...grpc.CallOption) (*ConnectServerStream, error)
	// ScheduleJob is used by the daprd sidecar to schedule a job.
	ScheduleJob(ctx context.Context, in *ScheduleJobRequest, opts ...grpc.CallOption) (*ScheduleJobResponse, error)
	// DeleteJob is used by the daprd sidecar to delete a job.
	DeleteJob(ctx context.Context, in *JobRequest, opts ...grpc.CallOption) (*DeleteJobResponse, error)
	// GetJob is used by the daprd sidecar to get details of a job.
	GetJob(ctx context.Context, in *JobRequest, opts ...grpc.CallOption) (*GetJobResponse, error)
	// ListJobs is used by the daprd sidecar to list jobs by app_id.
	ListJobs(ctx context.Context, in *ListJobsRequest, opts ...grpc.CallOption) (*ListJobsResponse, error)
}

type schedulerClient struct {
	cc grpc.ClientConnInterface
}

func NewSchedulerClient(cc grpc.ClientConnInterface) SchedulerClient {
	return &schedulerClient{cc}
}

func (c *schedulerClient) ConnectHost(ctx context.Context, in *ConnectClientStream, opts ...grpc.CallOption) (*ConnectServerStream, error) {
	out := new(ConnectServerStream)
	err := c.cc.Invoke(ctx, "/dapr.proto.scheduler.v1.Scheduler/ConnectHost", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *schedulerClient) ScheduleJob(ctx context.Context, in *ScheduleJobRequest, opts ...grpc.CallOption) (*ScheduleJobResponse, error) {
	out := new(ScheduleJobResponse)
	err := c.cc.Invoke(ctx, "/dapr.proto.scheduler.v1.Scheduler/ScheduleJob", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *schedulerClient) DeleteJob(ctx context.Context, in *JobRequest, opts ...grpc.CallOption) (*DeleteJobResponse, error) {
	out := new(DeleteJobResponse)
	err := c.cc.Invoke(ctx, "/dapr.proto.scheduler.v1.Scheduler/DeleteJob", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *schedulerClient) GetJob(ctx context.Context, in *JobRequest, opts ...grpc.CallOption) (*GetJobResponse, error) {
	out := new(GetJobResponse)
	err := c.cc.Invoke(ctx, "/dapr.proto.scheduler.v1.Scheduler/GetJob", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *schedulerClient) ListJobs(ctx context.Context, in *ListJobsRequest, opts ...grpc.CallOption) (*ListJobsResponse, error) {
	out := new(ListJobsResponse)
	err := c.cc.Invoke(ctx, "/dapr.proto.scheduler.v1.Scheduler/ListJobs", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// SchedulerServer is the server API for Scheduler service.
// All implementations should embed UnimplementedSchedulerServer
// for forward compatibility
type SchedulerServer interface {
	// ConnectHost is used by the daprd sidecar to connect to the scheduler service.
	// The sidecar will send a stream of RegisterRequest, UnregisterRequest and blank
	// health-check messages.
	// In return, scheduler service will send a stream of blank ConnectServerStream messages,
	// which will also act as health check messages.
	ConnectHost(context.Context, *ConnectClientStream) (*ConnectServerStream, error)
	// ScheduleJob is used by the daprd sidecar to schedule a job.
	ScheduleJob(context.Context, *ScheduleJobRequest) (*ScheduleJobResponse, error)
	// DeleteJob is used by the daprd sidecar to delete a job.
	DeleteJob(context.Context, *JobRequest) (*DeleteJobResponse, error)
	// GetJob is used by the daprd sidecar to get details of a job.
	GetJob(context.Context, *JobRequest) (*GetJobResponse, error)
	// ListJobs is used by the daprd sidecar to list jobs by app_id.
	ListJobs(context.Context, *ListJobsRequest) (*ListJobsResponse, error)
}

// UnimplementedSchedulerServer should be embedded to have forward compatible implementations.
type UnimplementedSchedulerServer struct {
}

func (UnimplementedSchedulerServer) ConnectHost(context.Context, *ConnectClientStream) (*ConnectServerStream, error) {
	return nil, status.Errorf(codes.Unimplemented, "method ConnectHost not implemented")
}
func (UnimplementedSchedulerServer) ScheduleJob(context.Context, *ScheduleJobRequest) (*ScheduleJobResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method ScheduleJob not implemented")
}
func (UnimplementedSchedulerServer) DeleteJob(context.Context, *JobRequest) (*DeleteJobResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method DeleteJob not implemented")
}
func (UnimplementedSchedulerServer) GetJob(context.Context, *JobRequest) (*GetJobResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method GetJob not implemented")
}
func (UnimplementedSchedulerServer) ListJobs(context.Context, *ListJobsRequest) (*ListJobsResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method ListJobs not implemented")
}

// UnsafeSchedulerServer may be embedded to opt out of forward compatibility for this service.
// Use of this interface is not recommended, as added methods to SchedulerServer will
// result in compilation errors.
type UnsafeSchedulerServer interface {
	mustEmbedUnimplementedSchedulerServer()
}

func RegisterSchedulerServer(s grpc.ServiceRegistrar, srv SchedulerServer) {
	s.RegisterService(&Scheduler_ServiceDesc, srv)
}

func _Scheduler_ConnectHost_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(ConnectClientStream)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(SchedulerServer).ConnectHost(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/dapr.proto.scheduler.v1.Scheduler/ConnectHost",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(SchedulerServer).ConnectHost(ctx, req.(*ConnectClientStream))
	}
	return interceptor(ctx, in, info, handler)
}

func _Scheduler_ScheduleJob_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(ScheduleJobRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(SchedulerServer).ScheduleJob(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/dapr.proto.scheduler.v1.Scheduler/ScheduleJob",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(SchedulerServer).ScheduleJob(ctx, req.(*ScheduleJobRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _Scheduler_DeleteJob_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(JobRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(SchedulerServer).DeleteJob(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/dapr.proto.scheduler.v1.Scheduler/DeleteJob",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(SchedulerServer).DeleteJob(ctx, req.(*JobRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _Scheduler_GetJob_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(JobRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(SchedulerServer).GetJob(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/dapr.proto.scheduler.v1.Scheduler/GetJob",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(SchedulerServer).GetJob(ctx, req.(*JobRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _Scheduler_ListJobs_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(ListJobsRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(SchedulerServer).ListJobs(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/dapr.proto.scheduler.v1.Scheduler/ListJobs",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(SchedulerServer).ListJobs(ctx, req.(*ListJobsRequest))
	}
	return interceptor(ctx, in, info, handler)
}

// Scheduler_ServiceDesc is the grpc.ServiceDesc for Scheduler service.
// It's only intended for direct use with grpc.RegisterService,
// and not to be introspected or modified (even as a copy)
var Scheduler_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "dapr.proto.scheduler.v1.Scheduler",
	HandlerType: (*SchedulerServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "ConnectHost",
			Handler:    _Scheduler_ConnectHost_Handler,
		},
		{
			MethodName: "ScheduleJob",
			Handler:    _Scheduler_ScheduleJob_Handler,
		},
		{
			MethodName: "DeleteJob",
			Handler:    _Scheduler_DeleteJob_Handler,
		},
		{
			MethodName: "GetJob",
			Handler:    _Scheduler_GetJob_Handler,
		},
		{
			MethodName: "ListJobs",
			Handler:    _Scheduler_ListJobs_Handler,
		},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: "dapr/proto/scheduler/v1/scheduler.proto",
}
