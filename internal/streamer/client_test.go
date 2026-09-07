package streamer

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	pb "github.com/syntrixbase/syntrix/api/gen/streamer/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

func TestDefaultClientConfig(t *testing.T) {
	cfg := DefaultClientConfig()

	assert.Equal(t, "localhost:50052", cfg.StreamerAddr)
	assert.NotZero(t, cfg.InitialBackoff)
	assert.NotZero(t, cfg.MaxBackoff)
	assert.NotZero(t, cfg.HeartbeatInterval)
	assert.NotZero(t, cfg.ActivityTimeout)
}

// mockGRPCStreamClient implements pb.StreamerService_StreamClient for testing.
type mockGRPCStreamClient struct {
	sentMsgs   []*pb.GatewayMessage
	recvMsgs   []*pb.StreamerMessage
	recvIdx    int
	recvErr    error
	sendErr    error
	closedSend bool
	mu         sync.Mutex
}

func (m *mockGRPCStreamClient) Send(msg *pb.GatewayMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sendErr != nil {
		return m.sendErr
	}
	m.sentMsgs = append(m.sentMsgs, msg)
	return nil
}

func (m *mockGRPCStreamClient) Recv() (*pb.StreamerMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.recvErr != nil {
		return nil, m.recvErr
	}
	if m.recvIdx >= len(m.recvMsgs) {
		return nil, io.EOF
	}
	msg := m.recvMsgs[m.recvIdx]
	m.recvIdx++
	return msg, nil
}

func (m *mockGRPCStreamClient) CloseSend() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closedSend = true
	return nil
}

func (m *mockGRPCStreamClient) Header() (metadata.MD, error) { return nil, nil }
func (m *mockGRPCStreamClient) Trailer() metadata.MD         { return nil }
func (m *mockGRPCStreamClient) Context() context.Context     { return context.Background() }
func (m *mockGRPCStreamClient) SendMsg(interface{}) error    { return nil }
func (m *mockGRPCStreamClient) RecvMsg(interface{}) error    { return nil }

// mockStreamerServiceClient implements pb.StreamerServiceClient for testing.
type mockStreamerServiceClient struct {
	pb.StreamerServiceClient
	streamClient pb.StreamerService_StreamClient
	streamErr    error
	streamCalls  int
}

func (m *mockStreamerServiceClient) Stream(ctx context.Context, opts ...grpc.CallOption) (pb.StreamerService_StreamClient, error) {
	m.streamCalls++
	if m.streamErr != nil {
		return nil, m.streamErr
	}
	return m.streamClient, nil
}

func TestStreamerClient_Stream_Success(t *testing.T) {
	// Create a client with mock service client
	mockStream := &mockGRPCStreamClient{
		recvMsgs: []*pb.StreamerMessage{
			{
				Payload: &pb.StreamerMessage_Delivery{
					Delivery: &pb.EventDelivery{
						SubscriptionIds: []string{"sub1"},
						Event:           &pb.StreamerEvent{Collection: "users"},
					},
				},
			},
		},
	}

	c := &streamerClient{
		client: &mockStreamerServiceClient{streamClient: mockStream},
		logger: slog.Default(),
	}

	stream, err := c.Stream(context.Background())
	require.NoError(t, err)
	require.NotNil(t, stream)

	// Verify it's a remoteStream
	rs, ok := stream.(*remoteStream)
	assert.True(t, ok)
	assert.NotNil(t, rs.grpcStream)
}

func TestRemoteStream_Subscribe_SendError(t *testing.T) {
	mockStream := &mockGRPCStreamClient{
		sendErr: errors.New("send failed"),
	}
	rs := &remoteStream{
		ctx:        context.Background(),
		grpcStream: mockStream,
		logger:     slog.Default(),
	}

	_, err := rs.Subscribe("database1", "users", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "send failed")
}

func TestRemoteStream_Subscribe_Closed(t *testing.T) {
	mockStream := &mockGRPCStreamClient{}
	rs := &remoteStream{
		ctx:        context.Background(),
		grpcStream: mockStream,
		logger:     slog.Default(),
		closed:     true,
	}

	_, err := rs.Subscribe("database1", "users", nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, io.EOF)
}

func TestRemoteStream_Subscribe_ContextCanceled(t *testing.T) {
	t.Parallel()
	mockStream := &mockGRPCStreamClient{}
	ctx, cancel := context.WithCancel(context.Background())
	rs := &remoteStream{
		ctx:        ctx,
		grpcStream: mockStream,
		logger:     slog.Default(),
	}

	// Start subscribe in goroutine
	done := make(chan error, 1)
	go func() {
		_, err := rs.Subscribe("database1", "users", nil)
		done <- err
	}()

	// Cancel context after a short delay
	time.Sleep(20 * time.Millisecond)
	cancel()

	err := <-done
	require.Error(t, err)
}

func TestRemoteStream_Unsubscribe(t *testing.T) {
	t.Parallel()
	mockStream := &mockGRPCStreamClient{}
	rs := &remoteStream{
		ctx:        context.Background(),
		grpcStream: mockStream,
		logger:     slog.Default(),
	}

	err := rs.Unsubscribe("sub123")
	require.NoError(t, err)

	assert.Len(t, mockStream.sentMsgs, 1)
	assert.NotNil(t, mockStream.sentMsgs[0].GetUnsubscribe())
	assert.Equal(t, "sub123", mockStream.sentMsgs[0].GetUnsubscribe().SubscriptionId)
}

func TestRemoteStream_Unsubscribe_Closed(t *testing.T) {
	mockStream := &mockGRPCStreamClient{}
	rs := &remoteStream{
		ctx:        context.Background(),
		grpcStream: mockStream,
		logger:     slog.Default(),
		closed:     true,
	}

	err := rs.Unsubscribe("sub123")
	require.Error(t, err)
	assert.ErrorIs(t, err, io.EOF)
}
