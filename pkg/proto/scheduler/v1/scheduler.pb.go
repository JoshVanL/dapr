// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.28.1
// 	protoc        v3.21.12
// source: dapr/proto/scheduler/v1/scheduler.proto

package scheduler

import (
	v1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	protoreflect "google.golang.org/protobuf/reflect/protoreflect"
	protoimpl "google.golang.org/protobuf/runtime/protoimpl"
	reflect "reflect"
	sync "sync"
)

const (
	// Verify that this generated code is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(20 - protoimpl.MinVersion)
	// Verify that runtime/protoimpl is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(protoimpl.MaxVersion - 20)
)

// ConnectClientStream is the message used by the daprd sidecar to connect to the scheduler service.
// The initiating message has to be a RegisterRequest, followed by a stream of blank health-check messages.
// The sidecar can send a UnregisterRequest to unregister itself from the scheduler service.
type ConnectClientStream struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// The message type is oneof, so that the sidecar can send a stream of RegisterRequest or UnregisterRequest
	// or blank health-check messages.
	//
	// Types that are assignable to Message:
	//
	//	*ConnectClientStream_Register
	//	*ConnectClientStream_Unregister
	Message isConnectClientStream_Message `protobuf_oneof:"message"`
}

func (x *ConnectClientStream) Reset() {
	*x = ConnectClientStream{}
	if protoimpl.UnsafeEnabled {
		mi := &file_dapr_proto_scheduler_v1_scheduler_proto_msgTypes[0]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *ConnectClientStream) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ConnectClientStream) ProtoMessage() {}

func (x *ConnectClientStream) ProtoReflect() protoreflect.Message {
	mi := &file_dapr_proto_scheduler_v1_scheduler_proto_msgTypes[0]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ConnectClientStream.ProtoReflect.Descriptor instead.
func (*ConnectClientStream) Descriptor() ([]byte, []int) {
	return file_dapr_proto_scheduler_v1_scheduler_proto_rawDescGZIP(), []int{0}
}

func (m *ConnectClientStream) GetMessage() isConnectClientStream_Message {
	if m != nil {
		return m.Message
	}
	return nil
}

func (x *ConnectClientStream) GetRegister() *RegisterRequest {
	if x, ok := x.GetMessage().(*ConnectClientStream_Register); ok {
		return x.Register
	}
	return nil
}

func (x *ConnectClientStream) GetUnregister() *UnregisterRequest {
	if x, ok := x.GetMessage().(*ConnectClientStream_Unregister); ok {
		return x.Unregister
	}
	return nil
}

type isConnectClientStream_Message interface {
	isConnectClientStream_Message()
}

type ConnectClientStream_Register struct {
	Register *RegisterRequest `protobuf:"bytes,1,opt,name=register,proto3,oneof"`
}

type ConnectClientStream_Unregister struct {
	Unregister *UnregisterRequest `protobuf:"bytes,2,opt,name=unregister,proto3,oneof"`
}

func (*ConnectClientStream_Register) isConnectClientStream_Message() {}

func (*ConnectClientStream_Unregister) isConnectClientStream_Message() {}

type ConnectServerStream struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields
}

func (x *ConnectServerStream) Reset() {
	*x = ConnectServerStream{}
	if protoimpl.UnsafeEnabled {
		mi := &file_dapr_proto_scheduler_v1_scheduler_proto_msgTypes[1]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *ConnectServerStream) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ConnectServerStream) ProtoMessage() {}

func (x *ConnectServerStream) ProtoReflect() protoreflect.Message {
	mi := &file_dapr_proto_scheduler_v1_scheduler_proto_msgTypes[1]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ConnectServerStream.ProtoReflect.Descriptor instead.
func (*ConnectServerStream) Descriptor() ([]byte, []int) {
	return file_dapr_proto_scheduler_v1_scheduler_proto_rawDescGZIP(), []int{1}
}

type RegisterRequest struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// The id of the application (app_id).
	AppId string `protobuf:"bytes,1,opt,name=app_id,json=appId,proto3" json:"app_id,omitempty"`
	// The namespace.
	Namespace string `protobuf:"bytes,2,opt,name=namespace,proto3" json:"namespace,omitempty"`
	// The hostname of the daprd sidecar.
	Hostname string `protobuf:"bytes,3,opt,name=hostname,proto3" json:"hostname,omitempty"`
	// The port of the daprd sidecar.
	Port int32 `protobuf:"varint,4,opt,name=port,proto3" json:"port,omitempty"`
}

func (x *RegisterRequest) Reset() {
	*x = RegisterRequest{}
	if protoimpl.UnsafeEnabled {
		mi := &file_dapr_proto_scheduler_v1_scheduler_proto_msgTypes[2]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *RegisterRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*RegisterRequest) ProtoMessage() {}

func (x *RegisterRequest) ProtoReflect() protoreflect.Message {
	mi := &file_dapr_proto_scheduler_v1_scheduler_proto_msgTypes[2]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use RegisterRequest.ProtoReflect.Descriptor instead.
func (*RegisterRequest) Descriptor() ([]byte, []int) {
	return file_dapr_proto_scheduler_v1_scheduler_proto_rawDescGZIP(), []int{2}
}

func (x *RegisterRequest) GetAppId() string {
	if x != nil {
		return x.AppId
	}
	return ""
}

func (x *RegisterRequest) GetNamespace() string {
	if x != nil {
		return x.Namespace
	}
	return ""
}

func (x *RegisterRequest) GetHostname() string {
	if x != nil {
		return x.Hostname
	}
	return ""
}

func (x *RegisterRequest) GetPort() int32 {
	if x != nil {
		return x.Port
	}
	return 0
}

// UnregisterRequest is the message used by the daprd sidecar to unregister itself from the scheduler service.
type UnregisterRequest struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// The id of the application (app_id).
	AppId string `protobuf:"bytes,1,opt,name=app_id,json=appId,proto3" json:"app_id,omitempty"`
	// The namespace.
	Namespace string `protobuf:"bytes,2,opt,name=namespace,proto3" json:"namespace,omitempty"`
	// The hostname of the daprd sidecar.
	Hostname string `protobuf:"bytes,3,opt,name=hostname,proto3" json:"hostname,omitempty"`
	// The port of the daprd sidecar.
	Port int32 `protobuf:"varint,4,opt,name=port,proto3" json:"port,omitempty"`
}

func (x *UnregisterRequest) Reset() {
	*x = UnregisterRequest{}
	if protoimpl.UnsafeEnabled {
		mi := &file_dapr_proto_scheduler_v1_scheduler_proto_msgTypes[3]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *UnregisterRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*UnregisterRequest) ProtoMessage() {}

func (x *UnregisterRequest) ProtoReflect() protoreflect.Message {
	mi := &file_dapr_proto_scheduler_v1_scheduler_proto_msgTypes[3]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use UnregisterRequest.ProtoReflect.Descriptor instead.
func (*UnregisterRequest) Descriptor() ([]byte, []int) {
	return file_dapr_proto_scheduler_v1_scheduler_proto_rawDescGZIP(), []int{3}
}

func (x *UnregisterRequest) GetAppId() string {
	if x != nil {
		return x.AppId
	}
	return ""
}

func (x *UnregisterRequest) GetNamespace() string {
	if x != nil {
		return x.Namespace
	}
	return ""
}

func (x *UnregisterRequest) GetHostname() string {
	if x != nil {
		return x.Hostname
	}
	return ""
}

func (x *UnregisterRequest) GetPort() int32 {
	if x != nil {
		return x.Port
	}
	return 0
}

type ScheduleJobRequest struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// The job to be scheduled.
	Job *v1.Job `protobuf:"bytes,1,opt,name=job,proto3" json:"job,omitempty"`
	// Namespace of the job
	Namespace string `protobuf:"bytes,2,opt,name=namespace,proto3" json:"namespace,omitempty"`
	// The metadata associated with the job.
	// The sidecar will create the unique `key` for storing data in the state store and pass the generated `key` along to the scheduler service for data lookup upon ‘trigger’ time later on.
	// The sidecar will also add metadata in order to know whether this job is registered for an actor. This is needed, as the routing mechanism for actors is different for the callback.
	Metadata map[string]string `protobuf:"bytes,3,rep,name=metadata,proto3" json:"metadata,omitempty" protobuf_key:"bytes,1,opt,name=key,proto3" protobuf_val:"bytes,2,opt,name=value,proto3"`
}

func (x *ScheduleJobRequest) Reset() {
	*x = ScheduleJobRequest{}
	if protoimpl.UnsafeEnabled {
		mi := &file_dapr_proto_scheduler_v1_scheduler_proto_msgTypes[4]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *ScheduleJobRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ScheduleJobRequest) ProtoMessage() {}

func (x *ScheduleJobRequest) ProtoReflect() protoreflect.Message {
	mi := &file_dapr_proto_scheduler_v1_scheduler_proto_msgTypes[4]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ScheduleJobRequest.ProtoReflect.Descriptor instead.
func (*ScheduleJobRequest) Descriptor() ([]byte, []int) {
	return file_dapr_proto_scheduler_v1_scheduler_proto_rawDescGZIP(), []int{4}
}

func (x *ScheduleJobRequest) GetJob() *v1.Job {
	if x != nil {
		return x.Job
	}
	return nil
}

func (x *ScheduleJobRequest) GetNamespace() string {
	if x != nil {
		return x.Namespace
	}
	return ""
}

func (x *ScheduleJobRequest) GetMetadata() map[string]string {
	if x != nil {
		return x.Metadata
	}
	return nil
}

type ScheduleJobResponse struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields
}

func (x *ScheduleJobResponse) Reset() {
	*x = ScheduleJobResponse{}
	if protoimpl.UnsafeEnabled {
		mi := &file_dapr_proto_scheduler_v1_scheduler_proto_msgTypes[5]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *ScheduleJobResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ScheduleJobResponse) ProtoMessage() {}

func (x *ScheduleJobResponse) ProtoReflect() protoreflect.Message {
	mi := &file_dapr_proto_scheduler_v1_scheduler_proto_msgTypes[5]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ScheduleJobResponse.ProtoReflect.Descriptor instead.
func (*ScheduleJobResponse) Descriptor() ([]byte, []int) {
	return file_dapr_proto_scheduler_v1_scheduler_proto_rawDescGZIP(), []int{5}
}

// JobRequest is the message used by the daprd sidecar to delete or get a job.
type JobRequest struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	JobName string `protobuf:"bytes,1,opt,name=job_name,json=jobName,proto3" json:"job_name,omitempty"`
}

func (x *JobRequest) Reset() {
	*x = JobRequest{}
	if protoimpl.UnsafeEnabled {
		mi := &file_dapr_proto_scheduler_v1_scheduler_proto_msgTypes[6]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *JobRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*JobRequest) ProtoMessage() {}

func (x *JobRequest) ProtoReflect() protoreflect.Message {
	mi := &file_dapr_proto_scheduler_v1_scheduler_proto_msgTypes[6]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use JobRequest.ProtoReflect.Descriptor instead.
func (*JobRequest) Descriptor() ([]byte, []int) {
	return file_dapr_proto_scheduler_v1_scheduler_proto_rawDescGZIP(), []int{6}
}

func (x *JobRequest) GetJobName() string {
	if x != nil {
		return x.JobName
	}
	return ""
}

type DeleteJobResponse struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields
}

func (x *DeleteJobResponse) Reset() {
	*x = DeleteJobResponse{}
	if protoimpl.UnsafeEnabled {
		mi := &file_dapr_proto_scheduler_v1_scheduler_proto_msgTypes[7]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *DeleteJobResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*DeleteJobResponse) ProtoMessage() {}

func (x *DeleteJobResponse) ProtoReflect() protoreflect.Message {
	mi := &file_dapr_proto_scheduler_v1_scheduler_proto_msgTypes[7]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use DeleteJobResponse.ProtoReflect.Descriptor instead.
func (*DeleteJobResponse) Descriptor() ([]byte, []int) {
	return file_dapr_proto_scheduler_v1_scheduler_proto_rawDescGZIP(), []int{7}
}

// GetJobResponse is the response message to convey the details of a job.
type GetJobResponse struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Job *v1.Job `protobuf:"bytes,1,opt,name=job,proto3" json:"job,omitempty"`
}

func (x *GetJobResponse) Reset() {
	*x = GetJobResponse{}
	if protoimpl.UnsafeEnabled {
		mi := &file_dapr_proto_scheduler_v1_scheduler_proto_msgTypes[8]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *GetJobResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*GetJobResponse) ProtoMessage() {}

func (x *GetJobResponse) ProtoReflect() protoreflect.Message {
	mi := &file_dapr_proto_scheduler_v1_scheduler_proto_msgTypes[8]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use GetJobResponse.ProtoReflect.Descriptor instead.
func (*GetJobResponse) Descriptor() ([]byte, []int) {
	return file_dapr_proto_scheduler_v1_scheduler_proto_rawDescGZIP(), []int{8}
}

func (x *GetJobResponse) GetJob() *v1.Job {
	if x != nil {
		return x.Job
	}
	return nil
}

// ListJobsRequest is the message to list jobs by app_id.
type ListJobsRequest struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// The id of the application (app_id) for which to list jobs.
	AppId string `protobuf:"bytes,1,opt,name=app_id,json=appId,proto3" json:"app_id,omitempty"`
}

func (x *ListJobsRequest) Reset() {
	*x = ListJobsRequest{}
	if protoimpl.UnsafeEnabled {
		mi := &file_dapr_proto_scheduler_v1_scheduler_proto_msgTypes[9]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *ListJobsRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ListJobsRequest) ProtoMessage() {}

func (x *ListJobsRequest) ProtoReflect() protoreflect.Message {
	mi := &file_dapr_proto_scheduler_v1_scheduler_proto_msgTypes[9]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ListJobsRequest.ProtoReflect.Descriptor instead.
func (*ListJobsRequest) Descriptor() ([]byte, []int) {
	return file_dapr_proto_scheduler_v1_scheduler_proto_rawDescGZIP(), []int{9}
}

func (x *ListJobsRequest) GetAppId() string {
	if x != nil {
		return x.AppId
	}
	return ""
}

// ListJobsResponse is the response message to convey the list of jobs.
type ListJobsResponse struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// List of jobs that match the request criteria.
	Jobs []*v1.Job `protobuf:"bytes,1,rep,name=jobs,proto3" json:"jobs,omitempty"`
}

func (x *ListJobsResponse) Reset() {
	*x = ListJobsResponse{}
	if protoimpl.UnsafeEnabled {
		mi := &file_dapr_proto_scheduler_v1_scheduler_proto_msgTypes[10]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *ListJobsResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ListJobsResponse) ProtoMessage() {}

func (x *ListJobsResponse) ProtoReflect() protoreflect.Message {
	mi := &file_dapr_proto_scheduler_v1_scheduler_proto_msgTypes[10]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ListJobsResponse.ProtoReflect.Descriptor instead.
func (*ListJobsResponse) Descriptor() ([]byte, []int) {
	return file_dapr_proto_scheduler_v1_scheduler_proto_rawDescGZIP(), []int{10}
}

func (x *ListJobsResponse) GetJobs() []*v1.Job {
	if x != nil {
		return x.Jobs
	}
	return nil
}

var File_dapr_proto_scheduler_v1_scheduler_proto protoreflect.FileDescriptor

var file_dapr_proto_scheduler_v1_scheduler_proto_rawDesc = []byte{
	0x0a, 0x27, 0x64, 0x61, 0x70, 0x72, 0x2f, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x2f, 0x73, 0x63, 0x68,
	0x65, 0x64, 0x75, 0x6c, 0x65, 0x72, 0x2f, 0x76, 0x31, 0x2f, 0x73, 0x63, 0x68, 0x65, 0x64, 0x75,
	0x6c, 0x65, 0x72, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x12, 0x17, 0x64, 0x61, 0x70, 0x72, 0x2e,
	0x70, 0x72, 0x6f, 0x74, 0x6f, 0x2e, 0x73, 0x63, 0x68, 0x65, 0x64, 0x75, 0x6c, 0x65, 0x72, 0x2e,
	0x76, 0x31, 0x1a, 0x20, 0x64, 0x61, 0x70, 0x72, 0x2f, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x2f, 0x72,
	0x75, 0x6e, 0x74, 0x69, 0x6d, 0x65, 0x2f, 0x76, 0x31, 0x2f, 0x64, 0x61, 0x70, 0x72, 0x2e, 0x70,
	0x72, 0x6f, 0x74, 0x6f, 0x22, 0xb6, 0x01, 0x0a, 0x13, 0x43, 0x6f, 0x6e, 0x6e, 0x65, 0x63, 0x74,
	0x43, 0x6c, 0x69, 0x65, 0x6e, 0x74, 0x53, 0x74, 0x72, 0x65, 0x61, 0x6d, 0x12, 0x46, 0x0a, 0x08,
	0x72, 0x65, 0x67, 0x69, 0x73, 0x74, 0x65, 0x72, 0x18, 0x01, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x28,
	0x2e, 0x64, 0x61, 0x70, 0x72, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x2e, 0x73, 0x63, 0x68, 0x65,
	0x64, 0x75, 0x6c, 0x65, 0x72, 0x2e, 0x76, 0x31, 0x2e, 0x52, 0x65, 0x67, 0x69, 0x73, 0x74, 0x65,
	0x72, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x48, 0x00, 0x52, 0x08, 0x72, 0x65, 0x67, 0x69,
	0x73, 0x74, 0x65, 0x72, 0x12, 0x4c, 0x0a, 0x0a, 0x75, 0x6e, 0x72, 0x65, 0x67, 0x69, 0x73, 0x74,
	0x65, 0x72, 0x18, 0x02, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x2a, 0x2e, 0x64, 0x61, 0x70, 0x72, 0x2e,
	0x70, 0x72, 0x6f, 0x74, 0x6f, 0x2e, 0x73, 0x63, 0x68, 0x65, 0x64, 0x75, 0x6c, 0x65, 0x72, 0x2e,
	0x76, 0x31, 0x2e, 0x55, 0x6e, 0x72, 0x65, 0x67, 0x69, 0x73, 0x74, 0x65, 0x72, 0x52, 0x65, 0x71,
	0x75, 0x65, 0x73, 0x74, 0x48, 0x00, 0x52, 0x0a, 0x75, 0x6e, 0x72, 0x65, 0x67, 0x69, 0x73, 0x74,
	0x65, 0x72, 0x42, 0x09, 0x0a, 0x07, 0x6d, 0x65, 0x73, 0x73, 0x61, 0x67, 0x65, 0x22, 0x15, 0x0a,
	0x13, 0x43, 0x6f, 0x6e, 0x6e, 0x65, 0x63, 0x74, 0x53, 0x65, 0x72, 0x76, 0x65, 0x72, 0x53, 0x74,
	0x72, 0x65, 0x61, 0x6d, 0x22, 0x76, 0x0a, 0x0f, 0x52, 0x65, 0x67, 0x69, 0x73, 0x74, 0x65, 0x72,
	0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x12, 0x15, 0x0a, 0x06, 0x61, 0x70, 0x70, 0x5f, 0x69,
	0x64, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x05, 0x61, 0x70, 0x70, 0x49, 0x64, 0x12, 0x1c,
	0x0a, 0x09, 0x6e, 0x61, 0x6d, 0x65, 0x73, 0x70, 0x61, 0x63, 0x65, 0x18, 0x02, 0x20, 0x01, 0x28,
	0x09, 0x52, 0x09, 0x6e, 0x61, 0x6d, 0x65, 0x73, 0x70, 0x61, 0x63, 0x65, 0x12, 0x1a, 0x0a, 0x08,
	0x68, 0x6f, 0x73, 0x74, 0x6e, 0x61, 0x6d, 0x65, 0x18, 0x03, 0x20, 0x01, 0x28, 0x09, 0x52, 0x08,
	0x68, 0x6f, 0x73, 0x74, 0x6e, 0x61, 0x6d, 0x65, 0x12, 0x12, 0x0a, 0x04, 0x70, 0x6f, 0x72, 0x74,
	0x18, 0x04, 0x20, 0x01, 0x28, 0x05, 0x52, 0x04, 0x70, 0x6f, 0x72, 0x74, 0x22, 0x78, 0x0a, 0x11,
	0x55, 0x6e, 0x72, 0x65, 0x67, 0x69, 0x73, 0x74, 0x65, 0x72, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73,
	0x74, 0x12, 0x15, 0x0a, 0x06, 0x61, 0x70, 0x70, 0x5f, 0x69, 0x64, 0x18, 0x01, 0x20, 0x01, 0x28,
	0x09, 0x52, 0x05, 0x61, 0x70, 0x70, 0x49, 0x64, 0x12, 0x1c, 0x0a, 0x09, 0x6e, 0x61, 0x6d, 0x65,
	0x73, 0x70, 0x61, 0x63, 0x65, 0x18, 0x02, 0x20, 0x01, 0x28, 0x09, 0x52, 0x09, 0x6e, 0x61, 0x6d,
	0x65, 0x73, 0x70, 0x61, 0x63, 0x65, 0x12, 0x1a, 0x0a, 0x08, 0x68, 0x6f, 0x73, 0x74, 0x6e, 0x61,
	0x6d, 0x65, 0x18, 0x03, 0x20, 0x01, 0x28, 0x09, 0x52, 0x08, 0x68, 0x6f, 0x73, 0x74, 0x6e, 0x61,
	0x6d, 0x65, 0x12, 0x12, 0x0a, 0x04, 0x70, 0x6f, 0x72, 0x74, 0x18, 0x04, 0x20, 0x01, 0x28, 0x05,
	0x52, 0x04, 0x70, 0x6f, 0x72, 0x74, 0x22, 0xf4, 0x01, 0x0a, 0x12, 0x53, 0x63, 0x68, 0x65, 0x64,
	0x75, 0x6c, 0x65, 0x4a, 0x6f, 0x62, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x12, 0x2c, 0x0a,
	0x03, 0x6a, 0x6f, 0x62, 0x18, 0x01, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x1a, 0x2e, 0x64, 0x61, 0x70,
	0x72, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x2e, 0x72, 0x75, 0x6e, 0x74, 0x69, 0x6d, 0x65, 0x2e,
	0x76, 0x31, 0x2e, 0x4a, 0x6f, 0x62, 0x52, 0x03, 0x6a, 0x6f, 0x62, 0x12, 0x1c, 0x0a, 0x09, 0x6e,
	0x61, 0x6d, 0x65, 0x73, 0x70, 0x61, 0x63, 0x65, 0x18, 0x02, 0x20, 0x01, 0x28, 0x09, 0x52, 0x09,
	0x6e, 0x61, 0x6d, 0x65, 0x73, 0x70, 0x61, 0x63, 0x65, 0x12, 0x55, 0x0a, 0x08, 0x6d, 0x65, 0x74,
	0x61, 0x64, 0x61, 0x74, 0x61, 0x18, 0x03, 0x20, 0x03, 0x28, 0x0b, 0x32, 0x39, 0x2e, 0x64, 0x61,
	0x70, 0x72, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x2e, 0x73, 0x63, 0x68, 0x65, 0x64, 0x75, 0x6c,
	0x65, 0x72, 0x2e, 0x76, 0x31, 0x2e, 0x53, 0x63, 0x68, 0x65, 0x64, 0x75, 0x6c, 0x65, 0x4a, 0x6f,
	0x62, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x2e, 0x4d, 0x65, 0x74, 0x61, 0x64, 0x61, 0x74,
	0x61, 0x45, 0x6e, 0x74, 0x72, 0x79, 0x52, 0x08, 0x6d, 0x65, 0x74, 0x61, 0x64, 0x61, 0x74, 0x61,
	0x1a, 0x3b, 0x0a, 0x0d, 0x4d, 0x65, 0x74, 0x61, 0x64, 0x61, 0x74, 0x61, 0x45, 0x6e, 0x74, 0x72,
	0x79, 0x12, 0x10, 0x0a, 0x03, 0x6b, 0x65, 0x79, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x03,
	0x6b, 0x65, 0x79, 0x12, 0x14, 0x0a, 0x05, 0x76, 0x61, 0x6c, 0x75, 0x65, 0x18, 0x02, 0x20, 0x01,
	0x28, 0x09, 0x52, 0x05, 0x76, 0x61, 0x6c, 0x75, 0x65, 0x3a, 0x02, 0x38, 0x01, 0x22, 0x15, 0x0a,
	0x13, 0x53, 0x63, 0x68, 0x65, 0x64, 0x75, 0x6c, 0x65, 0x4a, 0x6f, 0x62, 0x52, 0x65, 0x73, 0x70,
	0x6f, 0x6e, 0x73, 0x65, 0x22, 0x27, 0x0a, 0x0a, 0x4a, 0x6f, 0x62, 0x52, 0x65, 0x71, 0x75, 0x65,
	0x73, 0x74, 0x12, 0x19, 0x0a, 0x08, 0x6a, 0x6f, 0x62, 0x5f, 0x6e, 0x61, 0x6d, 0x65, 0x18, 0x01,
	0x20, 0x01, 0x28, 0x09, 0x52, 0x07, 0x6a, 0x6f, 0x62, 0x4e, 0x61, 0x6d, 0x65, 0x22, 0x13, 0x0a,
	0x11, 0x44, 0x65, 0x6c, 0x65, 0x74, 0x65, 0x4a, 0x6f, 0x62, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e,
	0x73, 0x65, 0x22, 0x3e, 0x0a, 0x0e, 0x47, 0x65, 0x74, 0x4a, 0x6f, 0x62, 0x52, 0x65, 0x73, 0x70,
	0x6f, 0x6e, 0x73, 0x65, 0x12, 0x2c, 0x0a, 0x03, 0x6a, 0x6f, 0x62, 0x18, 0x01, 0x20, 0x01, 0x28,
	0x0b, 0x32, 0x1a, 0x2e, 0x64, 0x61, 0x70, 0x72, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x2e, 0x72,
	0x75, 0x6e, 0x74, 0x69, 0x6d, 0x65, 0x2e, 0x76, 0x31, 0x2e, 0x4a, 0x6f, 0x62, 0x52, 0x03, 0x6a,
	0x6f, 0x62, 0x22, 0x28, 0x0a, 0x0f, 0x4c, 0x69, 0x73, 0x74, 0x4a, 0x6f, 0x62, 0x73, 0x52, 0x65,
	0x71, 0x75, 0x65, 0x73, 0x74, 0x12, 0x15, 0x0a, 0x06, 0x61, 0x70, 0x70, 0x5f, 0x69, 0x64, 0x18,
	0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x05, 0x61, 0x70, 0x70, 0x49, 0x64, 0x22, 0x42, 0x0a, 0x10,
	0x4c, 0x69, 0x73, 0x74, 0x4a, 0x6f, 0x62, 0x73, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65,
	0x12, 0x2e, 0x0a, 0x04, 0x6a, 0x6f, 0x62, 0x73, 0x18, 0x01, 0x20, 0x03, 0x28, 0x0b, 0x32, 0x1a,
	0x2e, 0x64, 0x61, 0x70, 0x72, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x2e, 0x72, 0x75, 0x6e, 0x74,
	0x69, 0x6d, 0x65, 0x2e, 0x76, 0x31, 0x2e, 0x4a, 0x6f, 0x62, 0x52, 0x04, 0x6a, 0x6f, 0x62, 0x73,
	0x32, 0x81, 0x04, 0x0a, 0x09, 0x53, 0x63, 0x68, 0x65, 0x64, 0x75, 0x6c, 0x65, 0x72, 0x12, 0x6b,
	0x0a, 0x0b, 0x43, 0x6f, 0x6e, 0x6e, 0x65, 0x63, 0x74, 0x48, 0x6f, 0x73, 0x74, 0x12, 0x2c, 0x2e,
	0x64, 0x61, 0x70, 0x72, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x2e, 0x73, 0x63, 0x68, 0x65, 0x64,
	0x75, 0x6c, 0x65, 0x72, 0x2e, 0x76, 0x31, 0x2e, 0x43, 0x6f, 0x6e, 0x6e, 0x65, 0x63, 0x74, 0x43,
	0x6c, 0x69, 0x65, 0x6e, 0x74, 0x53, 0x74, 0x72, 0x65, 0x61, 0x6d, 0x1a, 0x2c, 0x2e, 0x64, 0x61,
	0x70, 0x72, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x2e, 0x73, 0x63, 0x68, 0x65, 0x64, 0x75, 0x6c,
	0x65, 0x72, 0x2e, 0x76, 0x31, 0x2e, 0x43, 0x6f, 0x6e, 0x6e, 0x65, 0x63, 0x74, 0x53, 0x65, 0x72,
	0x76, 0x65, 0x72, 0x53, 0x74, 0x72, 0x65, 0x61, 0x6d, 0x22, 0x00, 0x12, 0x6a, 0x0a, 0x0b, 0x53,
	0x63, 0x68, 0x65, 0x64, 0x75, 0x6c, 0x65, 0x4a, 0x6f, 0x62, 0x12, 0x2b, 0x2e, 0x64, 0x61, 0x70,
	0x72, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x2e, 0x73, 0x63, 0x68, 0x65, 0x64, 0x75, 0x6c, 0x65,
	0x72, 0x2e, 0x76, 0x31, 0x2e, 0x53, 0x63, 0x68, 0x65, 0x64, 0x75, 0x6c, 0x65, 0x4a, 0x6f, 0x62,
	0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x1a, 0x2c, 0x2e, 0x64, 0x61, 0x70, 0x72, 0x2e, 0x70,
	0x72, 0x6f, 0x74, 0x6f, 0x2e, 0x73, 0x63, 0x68, 0x65, 0x64, 0x75, 0x6c, 0x65, 0x72, 0x2e, 0x76,
	0x31, 0x2e, 0x53, 0x63, 0x68, 0x65, 0x64, 0x75, 0x6c, 0x65, 0x4a, 0x6f, 0x62, 0x52, 0x65, 0x73,
	0x70, 0x6f, 0x6e, 0x73, 0x65, 0x22, 0x00, 0x12, 0x5e, 0x0a, 0x09, 0x44, 0x65, 0x6c, 0x65, 0x74,
	0x65, 0x4a, 0x6f, 0x62, 0x12, 0x23, 0x2e, 0x64, 0x61, 0x70, 0x72, 0x2e, 0x70, 0x72, 0x6f, 0x74,
	0x6f, 0x2e, 0x73, 0x63, 0x68, 0x65, 0x64, 0x75, 0x6c, 0x65, 0x72, 0x2e, 0x76, 0x31, 0x2e, 0x4a,
	0x6f, 0x62, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x1a, 0x2a, 0x2e, 0x64, 0x61, 0x70, 0x72,
	0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x2e, 0x73, 0x63, 0x68, 0x65, 0x64, 0x75, 0x6c, 0x65, 0x72,
	0x2e, 0x76, 0x31, 0x2e, 0x44, 0x65, 0x6c, 0x65, 0x74, 0x65, 0x4a, 0x6f, 0x62, 0x52, 0x65, 0x73,
	0x70, 0x6f, 0x6e, 0x73, 0x65, 0x22, 0x00, 0x12, 0x58, 0x0a, 0x06, 0x47, 0x65, 0x74, 0x4a, 0x6f,
	0x62, 0x12, 0x23, 0x2e, 0x64, 0x61, 0x70, 0x72, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x2e, 0x73,
	0x63, 0x68, 0x65, 0x64, 0x75, 0x6c, 0x65, 0x72, 0x2e, 0x76, 0x31, 0x2e, 0x4a, 0x6f, 0x62, 0x52,
	0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x1a, 0x27, 0x2e, 0x64, 0x61, 0x70, 0x72, 0x2e, 0x70, 0x72,
	0x6f, 0x74, 0x6f, 0x2e, 0x73, 0x63, 0x68, 0x65, 0x64, 0x75, 0x6c, 0x65, 0x72, 0x2e, 0x76, 0x31,
	0x2e, 0x47, 0x65, 0x74, 0x4a, 0x6f, 0x62, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x22,
	0x00, 0x12, 0x61, 0x0a, 0x08, 0x4c, 0x69, 0x73, 0x74, 0x4a, 0x6f, 0x62, 0x73, 0x12, 0x28, 0x2e,
	0x64, 0x61, 0x70, 0x72, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x2e, 0x73, 0x63, 0x68, 0x65, 0x64,
	0x75, 0x6c, 0x65, 0x72, 0x2e, 0x76, 0x31, 0x2e, 0x4c, 0x69, 0x73, 0x74, 0x4a, 0x6f, 0x62, 0x73,
	0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x1a, 0x29, 0x2e, 0x64, 0x61, 0x70, 0x72, 0x2e, 0x70,
	0x72, 0x6f, 0x74, 0x6f, 0x2e, 0x73, 0x63, 0x68, 0x65, 0x64, 0x75, 0x6c, 0x65, 0x72, 0x2e, 0x76,
	0x31, 0x2e, 0x4c, 0x69, 0x73, 0x74, 0x4a, 0x6f, 0x62, 0x73, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e,
	0x73, 0x65, 0x22, 0x00, 0x42, 0x37, 0x5a, 0x35, 0x67, 0x69, 0x74, 0x68, 0x75, 0x62, 0x2e, 0x63,
	0x6f, 0x6d, 0x2f, 0x64, 0x61, 0x70, 0x72, 0x2f, 0x64, 0x61, 0x70, 0x72, 0x2f, 0x70, 0x6b, 0x67,
	0x2f, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x2f, 0x73, 0x63, 0x68, 0x65, 0x64, 0x75, 0x6c, 0x65, 0x72,
	0x2f, 0x76, 0x31, 0x3b, 0x73, 0x63, 0x68, 0x65, 0x64, 0x75, 0x6c, 0x65, 0x72, 0x62, 0x06, 0x70,
	0x72, 0x6f, 0x74, 0x6f, 0x33,
}

var (
	file_dapr_proto_scheduler_v1_scheduler_proto_rawDescOnce sync.Once
	file_dapr_proto_scheduler_v1_scheduler_proto_rawDescData = file_dapr_proto_scheduler_v1_scheduler_proto_rawDesc
)

func file_dapr_proto_scheduler_v1_scheduler_proto_rawDescGZIP() []byte {
	file_dapr_proto_scheduler_v1_scheduler_proto_rawDescOnce.Do(func() {
		file_dapr_proto_scheduler_v1_scheduler_proto_rawDescData = protoimpl.X.CompressGZIP(file_dapr_proto_scheduler_v1_scheduler_proto_rawDescData)
	})
	return file_dapr_proto_scheduler_v1_scheduler_proto_rawDescData
}

var file_dapr_proto_scheduler_v1_scheduler_proto_msgTypes = make([]protoimpl.MessageInfo, 12)
var file_dapr_proto_scheduler_v1_scheduler_proto_goTypes = []interface{}{
	(*ConnectClientStream)(nil), // 0: dapr.proto.scheduler.v1.ConnectClientStream
	(*ConnectServerStream)(nil), // 1: dapr.proto.scheduler.v1.ConnectServerStream
	(*RegisterRequest)(nil),     // 2: dapr.proto.scheduler.v1.RegisterRequest
	(*UnregisterRequest)(nil),   // 3: dapr.proto.scheduler.v1.UnregisterRequest
	(*ScheduleJobRequest)(nil),  // 4: dapr.proto.scheduler.v1.ScheduleJobRequest
	(*ScheduleJobResponse)(nil), // 5: dapr.proto.scheduler.v1.ScheduleJobResponse
	(*JobRequest)(nil),          // 6: dapr.proto.scheduler.v1.JobRequest
	(*DeleteJobResponse)(nil),   // 7: dapr.proto.scheduler.v1.DeleteJobResponse
	(*GetJobResponse)(nil),      // 8: dapr.proto.scheduler.v1.GetJobResponse
	(*ListJobsRequest)(nil),     // 9: dapr.proto.scheduler.v1.ListJobsRequest
	(*ListJobsResponse)(nil),    // 10: dapr.proto.scheduler.v1.ListJobsResponse
	nil,                         // 11: dapr.proto.scheduler.v1.ScheduleJobRequest.MetadataEntry
	(*v1.Job)(nil),              // 12: dapr.proto.runtime.v1.Job
}
var file_dapr_proto_scheduler_v1_scheduler_proto_depIdxs = []int32{
	2,  // 0: dapr.proto.scheduler.v1.ConnectClientStream.register:type_name -> dapr.proto.scheduler.v1.RegisterRequest
	3,  // 1: dapr.proto.scheduler.v1.ConnectClientStream.unregister:type_name -> dapr.proto.scheduler.v1.UnregisterRequest
	12, // 2: dapr.proto.scheduler.v1.ScheduleJobRequest.job:type_name -> dapr.proto.runtime.v1.Job
	11, // 3: dapr.proto.scheduler.v1.ScheduleJobRequest.metadata:type_name -> dapr.proto.scheduler.v1.ScheduleJobRequest.MetadataEntry
	12, // 4: dapr.proto.scheduler.v1.GetJobResponse.job:type_name -> dapr.proto.runtime.v1.Job
	12, // 5: dapr.proto.scheduler.v1.ListJobsResponse.jobs:type_name -> dapr.proto.runtime.v1.Job
	0,  // 6: dapr.proto.scheduler.v1.Scheduler.ConnectHost:input_type -> dapr.proto.scheduler.v1.ConnectClientStream
	4,  // 7: dapr.proto.scheduler.v1.Scheduler.ScheduleJob:input_type -> dapr.proto.scheduler.v1.ScheduleJobRequest
	6,  // 8: dapr.proto.scheduler.v1.Scheduler.DeleteJob:input_type -> dapr.proto.scheduler.v1.JobRequest
	6,  // 9: dapr.proto.scheduler.v1.Scheduler.GetJob:input_type -> dapr.proto.scheduler.v1.JobRequest
	9,  // 10: dapr.proto.scheduler.v1.Scheduler.ListJobs:input_type -> dapr.proto.scheduler.v1.ListJobsRequest
	1,  // 11: dapr.proto.scheduler.v1.Scheduler.ConnectHost:output_type -> dapr.proto.scheduler.v1.ConnectServerStream
	5,  // 12: dapr.proto.scheduler.v1.Scheduler.ScheduleJob:output_type -> dapr.proto.scheduler.v1.ScheduleJobResponse
	7,  // 13: dapr.proto.scheduler.v1.Scheduler.DeleteJob:output_type -> dapr.proto.scheduler.v1.DeleteJobResponse
	8,  // 14: dapr.proto.scheduler.v1.Scheduler.GetJob:output_type -> dapr.proto.scheduler.v1.GetJobResponse
	10, // 15: dapr.proto.scheduler.v1.Scheduler.ListJobs:output_type -> dapr.proto.scheduler.v1.ListJobsResponse
	11, // [11:16] is the sub-list for method output_type
	6,  // [6:11] is the sub-list for method input_type
	6,  // [6:6] is the sub-list for extension type_name
	6,  // [6:6] is the sub-list for extension extendee
	0,  // [0:6] is the sub-list for field type_name
}

func init() { file_dapr_proto_scheduler_v1_scheduler_proto_init() }
func file_dapr_proto_scheduler_v1_scheduler_proto_init() {
	if File_dapr_proto_scheduler_v1_scheduler_proto != nil {
		return
	}
	if !protoimpl.UnsafeEnabled {
		file_dapr_proto_scheduler_v1_scheduler_proto_msgTypes[0].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*ConnectClientStream); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_dapr_proto_scheduler_v1_scheduler_proto_msgTypes[1].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*ConnectServerStream); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_dapr_proto_scheduler_v1_scheduler_proto_msgTypes[2].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*RegisterRequest); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_dapr_proto_scheduler_v1_scheduler_proto_msgTypes[3].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*UnregisterRequest); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_dapr_proto_scheduler_v1_scheduler_proto_msgTypes[4].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*ScheduleJobRequest); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_dapr_proto_scheduler_v1_scheduler_proto_msgTypes[5].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*ScheduleJobResponse); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_dapr_proto_scheduler_v1_scheduler_proto_msgTypes[6].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*JobRequest); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_dapr_proto_scheduler_v1_scheduler_proto_msgTypes[7].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*DeleteJobResponse); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_dapr_proto_scheduler_v1_scheduler_proto_msgTypes[8].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*GetJobResponse); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_dapr_proto_scheduler_v1_scheduler_proto_msgTypes[9].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*ListJobsRequest); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_dapr_proto_scheduler_v1_scheduler_proto_msgTypes[10].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*ListJobsResponse); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
	}
	file_dapr_proto_scheduler_v1_scheduler_proto_msgTypes[0].OneofWrappers = []interface{}{
		(*ConnectClientStream_Register)(nil),
		(*ConnectClientStream_Unregister)(nil),
	}
	type x struct{}
	out := protoimpl.TypeBuilder{
		File: protoimpl.DescBuilder{
			GoPackagePath: reflect.TypeOf(x{}).PkgPath(),
			RawDescriptor: file_dapr_proto_scheduler_v1_scheduler_proto_rawDesc,
			NumEnums:      0,
			NumMessages:   12,
			NumExtensions: 0,
			NumServices:   1,
		},
		GoTypes:           file_dapr_proto_scheduler_v1_scheduler_proto_goTypes,
		DependencyIndexes: file_dapr_proto_scheduler_v1_scheduler_proto_depIdxs,
		MessageInfos:      file_dapr_proto_scheduler_v1_scheduler_proto_msgTypes,
	}.Build()
	File_dapr_proto_scheduler_v1_scheduler_proto = out.File
	file_dapr_proto_scheduler_v1_scheduler_proto_rawDesc = nil
	file_dapr_proto_scheduler_v1_scheduler_proto_goTypes = nil
	file_dapr_proto_scheduler_v1_scheduler_proto_depIdxs = nil
}
