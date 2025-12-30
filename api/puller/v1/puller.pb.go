// Code generated from puller.proto. DO NOT EDIT.
// To regenerate: protoc --go_out=. --go-grpc_out=. api/proto/puller.proto

package pullerv1

import (
	"context"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// SubscribeRequest configures a subscription to the event stream.
type SubscribeRequest struct {
	// Consumer identifier for logging/monitoring only.
	ConsumerId string `protobuf:"bytes,1,opt,name=consumer_id,json=consumerId,proto3" json:"consumer_id,omitempty"`

	// Progress marker from last processed event.
	// Return events AFTER this marker (exclusive).
	After string `protobuf:"bytes,2,opt,name=after,proto3" json:"after,omitempty"`

	// Enable catch-up coalescing when consumer is behind.
	CoalesceOnCatchUp bool `protobuf:"varint,3,opt,name=coalesce_on_catch_up,json=coalesceOnCatchUp,proto3" json:"coalesce_on_catch_up,omitempty"`
}

func (x *SubscribeRequest) Reset()         { *x = SubscribeRequest{} }
func (x *SubscribeRequest) String() string { return fmt.Sprintf("%+v", *x) }
func (*SubscribeRequest) ProtoMessage()    {}

func (x *SubscribeRequest) GetConsumerId() string {
	if x != nil {
		return x.ConsumerId
	}
	return ""
}

func (x *SubscribeRequest) GetAfter() string {
	if x != nil {
		return x.After
	}
	return ""
}

func (x *SubscribeRequest) GetCoalesceOnCatchUp() bool {
	if x != nil {
		return x.CoalesceOnCatchUp
	}
	return false
}

// Event represents a normalized change event from the database.
type Event struct {
	// Unique event identifier.
	Id string `protobuf:"bytes,1,opt,name=id,proto3" json:"id,omitempty"`

	// Tenant identifier.
	Tenant string `protobuf:"bytes,2,opt,name=tenant,proto3" json:"tenant,omitempty"`

	// Collection name where the change occurred.
	Collection string `protobuf:"bytes,3,opt,name=collection,proto3" json:"collection,omitempty"`

	// Document identifier that was changed.
	DocumentId string `protobuf:"bytes,4,opt,name=document_id,json=documentId,proto3" json:"document_id,omitempty"`

	// Operation type: "insert", "update", "replace", or "delete".
	OperationType string `protobuf:"bytes,5,opt,name=operation_type,json=operationType,proto3" json:"operation_type,omitempty"`

	// Full document after the change (JSON encoded).
	FullDocument []byte `protobuf:"bytes,6,opt,name=full_document,json=fullDocument,proto3" json:"full_document,omitempty"`

	// Description of update changes (JSON encoded).
	UpdateDescription []byte `protobuf:"bytes,7,opt,name=update_description,json=updateDescription,proto3" json:"update_description,omitempty"`

	// MongoDB cluster timestamp.
	ClusterTime *ClusterTime `protobuf:"bytes,8,opt,name=cluster_time,json=clusterTime,proto3" json:"cluster_time,omitempty"`

	// Unix timestamp in milliseconds when the event was received.
	Timestamp int64 `protobuf:"varint,9,opt,name=timestamp,proto3" json:"timestamp,omitempty"`

	// Current progress marker (opaque string).
	Progress string `protobuf:"bytes,10,opt,name=progress,proto3" json:"progress,omitempty"`
}

func (x *Event) Reset()         { *x = Event{} }
func (x *Event) String() string { return fmt.Sprintf("%+v", *x) }
func (*Event) ProtoMessage()    {}

func (x *Event) GetId() string {
	if x != nil {
		return x.Id
	}
	return ""
}

func (x *Event) GetTenant() string {
	if x != nil {
		return x.Tenant
	}
	return ""
}

func (x *Event) GetCollection() string {
	if x != nil {
		return x.Collection
	}
	return ""
}

func (x *Event) GetDocumentId() string {
	if x != nil {
		return x.DocumentId
	}
	return ""
}

func (x *Event) GetOperationType() string {
	if x != nil {
		return x.OperationType
	}
	return ""
}

func (x *Event) GetFullDocument() []byte {
	if x != nil {
		return x.FullDocument
	}
	return nil
}

func (x *Event) GetUpdateDescription() []byte {
	if x != nil {
		return x.UpdateDescription
	}
	return nil
}

func (x *Event) GetClusterTime() *ClusterTime {
	if x != nil {
		return x.ClusterTime
	}
	return nil
}

func (x *Event) GetTimestamp() int64 {
	if x != nil {
		return x.Timestamp
	}
	return 0
}

func (x *Event) GetProgress() string {
	if x != nil {
		return x.Progress
	}
	return ""
}

// ClusterTime represents MongoDB cluster timestamp for ordering.
type ClusterTime struct {
	// Seconds since Unix epoch.
	T uint32 `protobuf:"varint,1,opt,name=t,proto3" json:"t,omitempty"`

	// Increment within the second.
	I uint32 `protobuf:"varint,2,opt,name=i,proto3" json:"i,omitempty"`
}

func (x *ClusterTime) Reset()         { *x = ClusterTime{} }
func (x *ClusterTime) String() string { return fmt.Sprintf("%+v", *x) }
func (*ClusterTime) ProtoMessage()    {}

func (x *ClusterTime) GetT() uint32 {
	if x != nil {
		return x.T
	}
	return 0
}

func (x *ClusterTime) GetI() uint32 {
	if x != nil {
		return x.I
	}
	return 0
}

// PullerServiceClient is the client API for PullerService.
type PullerServiceClient interface {
	// Subscribe to merged event stream from all backends.
	Subscribe(ctx context.Context, in *SubscribeRequest, opts ...grpc.CallOption) (PullerService_SubscribeClient, error)
}

type pullerServiceClient struct {
	cc grpc.ClientConnInterface
}

// NewPullerServiceClient creates a new client for PullerService.
func NewPullerServiceClient(cc grpc.ClientConnInterface) PullerServiceClient {
	return &pullerServiceClient{cc}
}

func (c *pullerServiceClient) Subscribe(ctx context.Context, in *SubscribeRequest, opts ...grpc.CallOption) (PullerService_SubscribeClient, error) {
	stream, err := c.cc.NewStream(ctx, &PullerService_ServiceDesc.Streams[0], "/syntrix.puller.v1.PullerService/Subscribe", opts...)
	if err != nil {
		return nil, err
	}
	x := &pullerServiceSubscribeClient{stream}
	if err := x.ClientStream.SendMsg(in); err != nil {
		return nil, err
	}
	if err := x.ClientStream.CloseSend(); err != nil {
		return nil, err
	}
	return x, nil
}

// PullerService_SubscribeClient is the client stream for Subscribe.
type PullerService_SubscribeClient interface {
	Recv() (*Event, error)
	grpc.ClientStream
}

type pullerServiceSubscribeClient struct {
	grpc.ClientStream
}

func (x *pullerServiceSubscribeClient) Recv() (*Event, error) {
	m := new(Event)
	if err := x.ClientStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

// PullerServiceServer is the server API for PullerService.
type PullerServiceServer interface {
	// Subscribe to merged event stream from all backends.
	Subscribe(*SubscribeRequest, PullerService_SubscribeServer) error
	mustEmbedUnimplementedPullerServiceServer()
}

// UnimplementedPullerServiceServer must be embedded to have forward compatible implementations.
type UnimplementedPullerServiceServer struct{}

func (UnimplementedPullerServiceServer) Subscribe(*SubscribeRequest, PullerService_SubscribeServer) error {
	return status.Errorf(codes.Unimplemented, "method Subscribe not implemented")
}

func (UnimplementedPullerServiceServer) mustEmbedUnimplementedPullerServiceServer() {}

// UnsafePullerServiceServer may be embedded to opt out of forward compatibility.
type UnsafePullerServiceServer interface {
	mustEmbedUnimplementedPullerServiceServer()
}

// PullerService_SubscribeServer is the server stream for Subscribe.
type PullerService_SubscribeServer interface {
	Send(*Event) error
	grpc.ServerStream
}

type pullerServiceSubscribeServer struct {
	grpc.ServerStream
}

func (x *pullerServiceSubscribeServer) Send(m *Event) error {
	return x.ServerStream.SendMsg(m)
}

func _PullerService_Subscribe_Handler(srv interface{}, stream grpc.ServerStream) error {
	m := new(SubscribeRequest)
	if err := stream.RecvMsg(m); err != nil {
		return err
	}
	return srv.(PullerServiceServer).Subscribe(m, &pullerServiceSubscribeServer{stream})
}

// RegisterPullerServiceServer registers the server implementation.
func RegisterPullerServiceServer(s grpc.ServiceRegistrar, srv PullerServiceServer) {
	s.RegisterService(&PullerService_ServiceDesc, srv)
}

// PullerService_ServiceDesc is the grpc.ServiceDesc for PullerService.
var PullerService_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "syntrix.puller.v1.PullerService",
	HandlerType: (*PullerServiceServer)(nil),
	Methods:     []grpc.MethodDesc{},
	Streams: []grpc.StreamDesc{
		{
			StreamName:    "Subscribe",
			Handler:       _PullerService_Subscribe_Handler,
			ServerStreams: true,
		},
	},
	Metadata: "api/proto/puller.proto",
}
