package grpc

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	pullerv1 "github.com/syntrixbase/syntrix/api/gen/puller/v1"
	"github.com/syntrixbase/syntrix/internal/core/storage"
	"github.com/syntrixbase/syntrix/internal/puller/config"
	"github.com/syntrixbase/syntrix/internal/puller/events"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	grpcapi "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestServerHeartbeatDoesNotReadAheadProgress(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	waiting := make(chan struct{})
	sub := &subscriptionFixture{initialProgress: "last-delivered", next: func(ctx context.Context) (*events.PullerEvent, error) {
		close(waiting)
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	cfg := config.DefaultConfig().GRPC
	cfg.HeartbeatInterval = time.Millisecond
	server := NewServer(cfg, sourceFunc(func(context.Context, events.SubscribeOptions) (events.Subscription, error) { return sub, nil }), nil)
	defer server.Shutdown()
	sent := make(chan *pullerv1.PullerEvent, 1)
	sends := 0
	done := make(chan error, 1)
	go func() {
		done <- server.Subscribe(&pullerv1.SubscribeRequest{After: "last-delivered"}, &streamFixture{ctx: ctx, send: func(evt *pullerv1.PullerEvent) error {
			sends++
			if sends == 1 {
				return nil
			}
			select {
			case sent <- evt:
			case <-ctx.Done():
			}
			return context.Canceled
		}})
	}()
	select {
	case <-waiting:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case evt := <-sent:
		assert.Nil(t, evt.ChangeEvent)
		assert.Equal(t, "last-delivered", evt.Progress)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func TestServerDomainFailuresHaveStableDetails(t *testing.T) {
	t.Parallel()
	cases := []struct {
		code   events.ErrorCode
		status codes.Code
	}{
		{events.CodeInvalidCursor, codes.InvalidArgument}, {events.CodeUnknownSource, codes.InvalidArgument},
		{events.CodeGenerationMismatch, codes.FailedPrecondition}, {events.CodeHistoryExpired, codes.FailedPrecondition},
		{events.CodePositionAhead, codes.InvalidArgument}, {events.CodeStorageFailure, codes.Internal},
		{events.CodeSourceUnavailable, codes.Unavailable}, {events.CodeContinuityLost, codes.FailedPrecondition},
		{events.CodeUnsupportedFormat, codes.FailedPrecondition}, {events.CodeOverloaded, codes.ResourceExhausted},
	}
	for _, tt := range cases {
		t.Run(string(tt.code), func(t *testing.T) {
			failure := &events.Error{Code: tt.code, Backend: "backend", SourceID: "source", Generation: "generation", DiscardedThrough: 8, CommittedThrough: 10, Cause: errors.New("private disk path")}
			server := NewServer(config.DefaultConfig().GRPC, sourceFunc(func(context.Context, events.SubscribeOptions) (events.Subscription, error) { return nil, failure }), nil)
			defer server.Shutdown()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			err := server.Subscribe(&pullerv1.SubscribeRequest{}, &streamFixture{ctx: ctx, header: func(metadata.MD) error { t.Error("must not acknowledge rejected subscription"); return nil }})
			st := status.Convert(err)
			assert.Equal(t, tt.status, st.Code())
			assert.NotContains(t, st.Message(), "private disk path")
			require.Len(t, st.Details(), 1)
			detail, ok := st.Details()[0].(*errdetails.ErrorInfo)
			require.True(t, ok)
			assert.Equal(t, "syntrix.puller", detail.Domain)
			assert.Equal(t, string(tt.code), detail.Reason)
			assert.Equal(t, map[string]string{"backend": "backend", "source_id": "source", "generation": "generation", "discarded_through": "8", "committed_through": "10"}, detail.Metadata)
		})
	}
}

type sendObservedServer struct {
	pullerv1.UnimplementedPullerServiceServer
	adapter  *Server
	entered  chan struct{}
	returned chan struct{}
}

func (s *sendObservedServer) Subscribe(req *pullerv1.SubscribeRequest, stream pullerv1.PullerService_SubscribeServer) error {
	return s.adapter.Subscribe(req, &sendObservedStream{PullerService_SubscribeServer: stream, entered: s.entered, returned: s.returned})
}

type sendObservedStream struct {
	pullerv1.PullerService_SubscribeServer
	entered    chan struct{}
	returned   chan struct{}
	largeSends int
}

func (s *sendObservedStream) Send(event *pullerv1.PullerEvent) error {
	if event.ChangeEvent == nil {
		return s.PullerService_SubscribeServer.Send(event)
	}
	s.largeSends++
	// The first message spends the initial write quota; the second waits for reads.
	blocked := s.largeSends == 2
	if blocked {
		close(s.entered)
	}
	err := s.PullerService_SubscribeServer.Send(event)
	if blocked {
		close(s.returned)
	}
	return err
}

func TestShutdownReleasesFlowControlledSend(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	large := &events.PullerEvent{Change: &events.StoreChangeEvent{EventID: "large", FullDocument: &storage.StoredDoc{Data: map[string]any{"payload": strings.Repeat("x", 8<<20)}}}, Progress: "p1"}
	sub := &subscriptionFixture{next: func(context.Context) (*events.PullerEvent, error) { return large, nil }}
	adapter := NewServer(config.DefaultConfig().GRPC, sourceFunc(func(context.Context, events.SubscribeOptions) (events.Subscription, error) { return sub, nil }), nil)
	observed := &sendObservedServer{adapter: adapter, entered: make(chan struct{}), returned: make(chan struct{})}
	server := grpcapi.NewServer()
	pullerv1.RegisterPullerServiceServer(server, observed)
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()
	defer func() { adapter.Shutdown(); server.Stop(); require.NoError(t, <-serveDone) }()
	connection, err := grpcapi.NewClient(listener.Addr().String(), grpcapi.WithTransportCredentials(insecure.NewCredentials()), grpcapi.WithStaticStreamWindowSize(65535), grpcapi.WithStaticConnWindowSize(65535))
	require.NoError(t, err)
	defer connection.Close()
	stream, err := pullerv1.NewPullerServiceClient(connection).Subscribe(ctx, &pullerv1.SubscribeRequest{})
	require.NoError(t, err)
	_, err = stream.Header()
	require.NoError(t, err)
	select {
	case <-observed.entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case <-observed.returned:
		t.Fatal("large send unexpectedly completed without reads")
	default:
	}
	adapter.Shutdown()
	gracefulDone := make(chan struct{})
	go func() { server.GracefulStop(); close(gracefulDone) }()
	select {
	case <-observed.returned:
	case <-ctx.Done():
		t.Fatal("send worker did not terminate")
	}
	assert.Zero(t, adapter.SubscriberCount())
	assert.EqualValues(t, 1, sub.closed.Load())
	// Transport drain still waits for unread DATA before trailers; the unified
	// server's deadline may force Stop when a client does not release the socket.
	require.NoError(t, connection.Close())
	select {
	case <-gracefulDone:
	case <-ctx.Done():
		t.Fatal("graceful stop did not finish after connection release")
	}
}
