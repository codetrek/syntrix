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
	"github.com/syntrixbase/syntrix/pkg/model"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestRemoteStream_Unsubscribe_SendError(t *testing.T) {
	mockStream := &mockGRPCStreamClient{
		sendErr: errors.New("send failed"),
	}
	rs := &remoteStream{
		ctx:        context.Background(),
		grpcStream: mockStream,
		logger:     slog.Default(),
	}

	err := rs.Unsubscribe("sub123")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "send failed")
}

func TestRemoteStream_Recv_EventDelivery(t *testing.T) {
	mockStream := &mockGRPCStreamClient{
		recvMsgs: []*pb.StreamerMessage{
			{
				Payload: &pb.StreamerMessage_Delivery{
					Delivery: &pb.EventDelivery{
						SubscriptionIds: []string{"sub1"},
						Event: &pb.StreamerEvent{
							EventId:    "evt1",
							Database:   "database1",
							Collection: "users",
							Operation:  pb.OperationType_OPERATION_TYPE_INSERT,
						},
					},
				},
			},
		},
	}
	rs := &remoteStream{
		ctx:        context.Background(),
		grpcStream: mockStream,
		logger:     slog.Default(),
	}

	delivery, err := rs.Recv()
	require.NoError(t, err)
	require.NotNil(t, delivery)
	assert.Equal(t, []string{"sub1"}, delivery.SubscriptionIDs)
	assert.Equal(t, "evt1", delivery.Event.EventID)
}

func TestRemoteStream_Recv_SkipsHeartbeatAck(t *testing.T) {
	mockStream := &mockGRPCStreamClient{
		recvMsgs: []*pb.StreamerMessage{
			{
				Payload: &pb.StreamerMessage_HeartbeatAck{
					HeartbeatAck: &pb.HeartbeatAck{},
				},
			},
			{
				Payload: &pb.StreamerMessage_Delivery{
					Delivery: &pb.EventDelivery{
						SubscriptionIds: []string{"sub1"},
						Event: &pb.StreamerEvent{
							EventId: "evt1",
						},
					},
				},
			},
		},
	}
	rs := &remoteStream{
		ctx:        context.Background(),
		grpcStream: mockStream,
		logger:     slog.Default(),
	}

	// Should skip heartbeat and return delivery
	delivery, err := rs.Recv()
	require.NoError(t, err)
	require.NotNil(t, delivery)
	assert.Equal(t, "evt1", delivery.Event.EventID)
}

func TestRemoteStream_Recv_EOF(t *testing.T) {
	mockStream := &mockGRPCStreamClient{
		recvMsgs: []*pb.StreamerMessage{},
	}
	rs := &remoteStream{
		ctx:        context.Background(),
		grpcStream: mockStream,
		logger:     slog.Default(),
	}

	_, err := rs.Recv()
	require.Error(t, err)
	assert.ErrorIs(t, err, io.EOF)
}

func TestRemoteStream_Recv_Error(t *testing.T) {
	mockStream := &mockGRPCStreamClient{
		recvErr: errors.New("recv failed"),
	}
	rs := &remoteStream{
		ctx:        context.Background(),
		grpcStream: mockStream,
		logger:     slog.Default(),
	}

	_, err := rs.Recv()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "recv failed")
}

func TestRemoteStream_Close(t *testing.T) {
	mockStream := &mockGRPCStreamClient{}
	rs := &remoteStream{
		ctx:        context.Background(),
		grpcStream: mockStream,
		logger:     slog.Default(),
	}

	// First close should work
	err := rs.Close()
	require.NoError(t, err)
	assert.True(t, mockStream.closedSend)

	// Second close should be no-op (idempotent)
	err = rs.Close()
	require.NoError(t, err)
}

func TestRemoteStream_HandleSubscribeResponse(t *testing.T) {
	rs := &remoteStream{
		ctx:               context.Background(),
		logger:            slog.Default(),
		pendingSubscribes: make(map[string]chan *pb.SubscribeResponse),
	}

	// Create a pending subscribe
	respChan := make(chan *pb.SubscribeResponse, 1)
	rs.pendingSubscribes["sub1"] = respChan

	// Handle response
	rs.handleSubscribeResponse(&pb.SubscribeResponse{
		SubscriptionId: "sub1",
		Success:        true,
	})

	// Verify response was routed
	select {
	case resp := <-respChan:
		assert.True(t, resp.Success)
		assert.Equal(t, "sub1", resp.SubscriptionId)
	case <-time.After(time.Second):
		t.Fatal("expected response")
	}
}

func TestRemoteStream_HandleSubscribeResponse_NoPending(t *testing.T) {
	rs := &remoteStream{
		ctx:               context.Background(),
		logger:            slog.Default(),
		pendingSubscribes: make(map[string]chan *pb.SubscribeResponse),
	}

	// Should not panic with no pending
	rs.handleSubscribeResponse(&pb.SubscribeResponse{
		SubscriptionId: "unknown",
		Success:        true,
	})
}

// --- NewClient and Stream Tests ---

func TestNewClient_Success(t *testing.T) {
	config := ClientConfig{
		StreamerAddr: "localhost:50099", // doesn't need to be running
	}

	client, err := NewClient(config, nil)
	require.NoError(t, err)
	require.NotNil(t, client)

	// Cleanup
	err = client.(*streamerClient).Close()
	assert.NoError(t, err)
}

func TestNewClient_DefaultValues(t *testing.T) {
	config := ClientConfig{
		StreamerAddr: "localhost:50099",
		// Leave intervals at zero to test defaults
	}

	client, err := NewClient(config, slog.Default())
	require.NoError(t, err)
	require.NotNil(t, client)

	c := client.(*streamerClient)
	assert.Equal(t, 1*time.Second, c.config.InitialBackoff)
	assert.Equal(t, 30*time.Second, c.config.MaxBackoff)
	assert.Equal(t, 30*time.Second, c.config.HeartbeatInterval)
	assert.Equal(t, 90*time.Second, c.config.ActivityTimeout)

	c.Close()
}

func TestStreamerClient_Close(t *testing.T) {
	config := ClientConfig{
		StreamerAddr: "localhost:50099",
	}

	client, err := NewClient(config, nil)
	require.NoError(t, err)

	c := client.(*streamerClient)

	// Close should work
	err = c.Close()
	assert.NoError(t, err)

	// Close again should also work (conn already closed)
	err = c.Close()
	assert.Error(t, err) // Already closed
}

func TestStreamerClient_Close_NilConn(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	c := &streamerClient{
		ctx:    ctx,
		cancel: cancel,
		conn:   nil, // No connection
	}

	// Close should return nil when conn is nil
	err := c.Close()
	assert.NoError(t, err)
}

func TestStreamerClient_Stream_NoServer(t *testing.T) {
	config := ClientConfig{
		StreamerAddr: "localhost:50099", // no server running
	}

	client, err := NewClient(config, nil)
	require.NoError(t, err)
	defer client.(*streamerClient).Close()

	// Stream should fail because no server is listening
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err = client.Stream(ctx)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to establish stream")
}

func TestRemoteStream_Recv_SkipsSubscribeResponse(t *testing.T) {
	// Send a SubscribeResponse followed by a Delivery
	mockStream := &mockGRPCStreamClient{
		recvMsgs: []*pb.StreamerMessage{
			{
				Payload: &pb.StreamerMessage_SubscribeResponse{
					SubscribeResponse: &pb.SubscribeResponse{
						SubscriptionId: "sub1",
						Success:        true,
					},
				},
			},
			{
				Payload: &pb.StreamerMessage_Delivery{
					Delivery: &pb.EventDelivery{
						SubscriptionIds: []string{"sub1"},
						Event:           &pb.StreamerEvent{Collection: "orders"},
					},
				},
			},
		},
	}

	rs := &remoteStream{
		ctx:               context.Background(),
		grpcStream:        mockStream,
		logger:            slog.Default(),
		pendingSubscribes: make(map[string]chan *pb.SubscribeResponse),
	}

	// Recv should skip SubscribeResponse and return the delivery
	delivery, err := rs.Recv()
	require.NoError(t, err)
	assert.Equal(t, "orders", delivery.Event.Collection)
}

// TestRemoteStream_Recv_ChannelClosedNoError tests Recv when channel closes without error (EOF).
func TestRemoteStream_Recv_ChannelClosedNoError(t *testing.T) {
	rs := &remoteStream{
		ctx:               context.Background(),
		grpcStream:        &mockGRPCStreamClient{},
		logger:            slog.Default(),
		recvChan:          make(chan *EventDelivery),
		pendingSubscribes: make(map[string]chan *pb.SubscribeResponse),
		recvErr:           nil, // No prior error
	}

	// Close the channel
	close(rs.recvChan)

	// Should return io.EOF
	_, err := rs.Recv()
	require.Error(t, err)
	assert.ErrorIs(t, err, io.EOF)
}

// TestRemoteStream_Subscribe_FailedResponse tests Subscribe when server returns failure.
func TestRemoteStream_Subscribe_FailedResponse(t *testing.T) {
	mockStream := &mockGRPCStreamClient{
		recvMsgs: []*pb.StreamerMessage{
			// Server will respond with failure
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rs := &remoteStream{
		ctx:               ctx,
		grpcStream:        mockStream,
		logger:            slog.Default(),
		pendingSubscribes: make(map[string]chan *pb.SubscribeResponse),
	}

	// Start a goroutine to inject the failure response
	go func() {
		time.Sleep(20 * time.Millisecond)
		// Find the pending subscription and send a failure response
		rs.pendingSubscribesMu.Lock()
		for subID, ch := range rs.pendingSubscribes {
			select {
			case ch <- &pb.SubscribeResponse{
				SubscriptionId: subID,
				Success:        false,
				Error:          "subscription denied",
			}:
			default:
			}
		}
		rs.pendingSubscribesMu.Unlock()
	}()

	_, err := rs.Subscribe("database1", "denied-collection", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "subscribe failed")
}

// TestRemoteStream_RecvLoop_DeliveryContextDone tests recv loop exits on context done.
func TestRemoteStream_RecvLoop_DeliveryContextDone(t *testing.T) {
	// Create a mock that blocks on recv
	mockStream := &mockGRPCStreamClient{
		recvMsgs: []*pb.StreamerMessage{
			{
				Payload: &pb.StreamerMessage_Delivery{
					Delivery: &pb.EventDelivery{
						SubscriptionIds: []string{"sub1"},
						Event:           &pb.StreamerEvent{Collection: "blocking"},
					},
				},
			},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	rs := &remoteStream{
		ctx:               ctx,
		grpcStream:        mockStream,
		logger:            slog.Default(),
		pendingSubscribes: make(map[string]chan *pb.SubscribeResponse),
		recvChan:          make(chan *EventDelivery), // Unbuffered - will block
	}

	// Start recv loop
	go rs.recvLoop()

	// Give it time to start
	time.Sleep(10 * time.Millisecond)

	// Cancel context while delivery is blocked
	cancel()

	// Give recv loop time to exit
	time.Sleep(30 * time.Millisecond)
}

// blockingMockGRPCStreamClient blocks until context is cancelled
type blockingMockGRPCStreamClient struct {
	mockGRPCStreamClient
	ctx context.Context
}

func (m *blockingMockGRPCStreamClient) Recv() (*pb.StreamerMessage, error) {
	// Block until context is done
	<-m.ctx.Done()
	return nil, m.ctx.Err()
}

// TestRemoteStream_Recv_ContextDoneAfterLoop tests Recv when context is cancelled after recv loop starts.
func TestRemoteStream_Recv_ContextDoneAfterLoop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	// Use a blocking mock that won't return until context is cancelled
	blockingMock := &blockingMockGRPCStreamClient{ctx: ctx}

	rs := &remoteStream{
		ctx:        ctx,
		grpcStream: blockingMock,
		logger:     slog.Default(),
	}

	done := make(chan error, 1)
	go func() {
		_, err := rs.Recv()
		done <- err
	}()

	// Give recv loop time to start
	time.Sleep(20 * time.Millisecond)

	// Cancel context - this should cause Recv to return via ctx.Done() case
	cancel()

	select {
	case err := <-done:
		require.Error(t, err)
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("Recv should have returned after context cancellation")
	}
}

// Note: Full client tests require a running gRPC server.
// Integration tests are in tests/integration/

// TestConnectionState_String tests the String() method of ConnectionState.
func TestConnectionState_String(t *testing.T) {
	tests := []struct {
		state    ConnectionState
		expected string
	}{
		{StateDisconnected, "disconnected"},
		{StateConnecting, "connecting"},
		{StateConnected, "connected"},
		{StateReconnecting, "reconnecting"},
		{ConnectionState(99), "unknown"},
	}

	for _, tc := range tests {
		t.Run(tc.expected, func(t *testing.T) {
			assert.Equal(t, tc.expected, tc.state.String())
		})
	}
}

// TestRemoteStream_State tests the State() method.
func TestRemoteStream_State(t *testing.T) {
	rs := &remoteStream{
		state:  StateConnected,
		logger: slog.Default(),
	}

	assert.Equal(t, StateConnected, rs.State())
}

// TestRemoteStream_GetLastMessageTime tests activity time tracking.
func TestRemoteStream_GetLastMessageTime(t *testing.T) {
	rs := &remoteStream{
		logger: slog.Default(),
	}

	// Initially zero
	assert.True(t, rs.getLastMessageTime().IsZero())

	// Update
	rs.updateLastMessageTime()
	now := time.Now()

	lastTime := rs.getLastMessageTime()
	assert.WithinDuration(t, now, lastTime, 10*time.Millisecond)
}

// TestRemoteStream_SetState tests setState and notifyStateWithError.
func TestRemoteStream_SetState(t *testing.T) {
	rs := &remoteStream{
		state:  StateConnected,
		logger: slog.Default(),
	}

	// Test setState
	rs.setState(StateReconnecting)
	assert.Equal(t, StateReconnecting, rs.State())

	// Test same state (no change)
	rs.setState(StateReconnecting)
	assert.Equal(t, StateReconnecting, rs.State())

	// Test notifyStateWithError
	rs.notifyStateWithError(StateDisconnected, io.EOF)
	assert.Equal(t, StateDisconnected, rs.State())
}

// TestRemoteStream_SetState_WithCallback tests state change callback.
func TestRemoteStream_SetState_WithCallback(t *testing.T) {
	var calledState ConnectionState
	var calledErr error
	callback := func(state ConnectionState, err error) {
		calledState = state
		calledErr = err
	}

	client := &streamerClient{
		config: ClientConfig{
			OnStateChange: callback,
		},
	}

	rs := &remoteStream{
		state:  StateConnected,
		logger: slog.Default(),
		client: client,
	}

	// Test setState triggers callback
	rs.setState(StateReconnecting)
	assert.Equal(t, StateReconnecting, calledState)
	assert.Nil(t, calledErr)

	// Test notifyStateWithError triggers callback with error
	testErr := errors.New("test error")
	rs.notifyStateWithError(StateDisconnected, testErr)
	assert.Equal(t, StateDisconnected, calledState)
	assert.Equal(t, testErr, calledErr)
}

// TestRemoteStream_NoClient tests functions exit immediately when client is nil.
func TestRemoteStream_NoClient(t *testing.T) {
	t.Parallel()

	t.Run("reconnect", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		rs := &remoteStream{
			ctx:    ctx,
			logger: slog.Default(),
			client: nil,
		}
		assert.False(t, rs.reconnect())
	})

	t.Run("heartbeatLoop", func(t *testing.T) {
		rs := &remoteStream{
			logger: slog.Default(),
			client: nil,
		}

		done := make(chan struct{})
		go func() {
			rs.heartbeatLoop()
			close(done)
		}()

		select {
		case <-done:
		case <-time.After(100 * time.Millisecond):
			t.Fatal("heartbeatLoop should have exited immediately when client is nil")
		}
	})

	t.Run("activityMonitor", func(t *testing.T) {
		rs := &remoteStream{
			logger: slog.Default(),
			client: nil,
		}

		done := make(chan struct{})
		go func() {
			rs.activityMonitor()
			close(done)
		}()

		select {
		case <-done:
		case <-time.After(100 * time.Millisecond):
			t.Fatal("activityMonitor should have exited immediately when client is nil")
		}
	})
}

// TestRemoteStream_Reconnect_ContextCanceled tests reconnect exits when context is canceled.
func TestRemoteStream_Reconnect_ContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	client := &streamerClient{
		config: ClientConfig{
			InitialBackoff:    10 * time.Millisecond,
			MaxBackoff:        100 * time.Millisecond,
			BackoffMultiplier: 2.0,
			MaxRetries:        0,
		},
	}

	rs := &remoteStream{
		ctx:    ctx,
		logger: slog.Default(),
		client: client,
	}

	// reconnect should return false immediately due to canceled context
	assert.False(t, rs.reconnect())
}

// TestRemoteStream_Reconnect_MaxRetries tests reconnect gives up after max retries.
func TestRemoteStream_Reconnect_MaxRetries(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	attempts := 0
	mockServiceClient := &mockStreamerServiceClient{
		streamErr: errors.New("connection refused"),
	}

	client := &streamerClient{
		config: ClientConfig{
			InitialBackoff:    1 * time.Millisecond,
			MaxBackoff:        5 * time.Millisecond,
			BackoffMultiplier: 1.5,
			MaxRetries:        3, // Max 3 attempts
		},
		client: mockServiceClient,
	}

	rs := &remoteStream{
		ctx:    ctx,
		logger: slog.Default(),
		client: client,
	}

	start := time.Now()
	result := rs.reconnect()
	elapsed := time.Since(start)

	assert.False(t, result)
	// Should have tried and given up relatively quickly
	assert.Less(t, elapsed, 500*time.Millisecond)
	_ = attempts // Used to verify attempts if needed
}

// TestRemoteStream_Reconnect_Success tests successful reconnection.
func TestRemoteStream_Reconnect_Success(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	newMockStream := &mockGRPCStreamClient{}
	mockServiceClient := &mockStreamerServiceClient{
		streamClient: newMockStream,
	}

	client := &streamerClient{
		config: ClientConfig{
			InitialBackoff:    1 * time.Millisecond,
			MaxBackoff:        10 * time.Millisecond,
			BackoffMultiplier: 2.0,
			MaxRetries:        0,
		},
		client: mockServiceClient,
	}

	rs := &remoteStream{
		ctx:                 ctx,
		logger:              slog.Default(),
		client:              client,
		activeSubscriptions: make(map[string]*subscriptionInfo),
	}

	result := rs.reconnect()

	assert.True(t, result)
	assert.Equal(t, StateConnected, rs.State())
}

// TestRemoteStream_Reconnect_WithSubscriptions tests reconnection with subscription recovery.
func TestRemoteStream_Reconnect_WithSubscriptions(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	newMockStream := &mockGRPCStreamClient{}
	mockServiceClient := &mockStreamerServiceClient{
		streamClient: newMockStream,
	}

	client := &streamerClient{
		config: ClientConfig{
			InitialBackoff:    1 * time.Millisecond,
			MaxBackoff:        10 * time.Millisecond,
			BackoffMultiplier: 2.0,
			MaxRetries:        0,
		},
		client: mockServiceClient,
	}

	rs := &remoteStream{
		ctx:    ctx,
		logger: slog.Default(),
		client: client,
		activeSubscriptions: map[string]*subscriptionInfo{
			"sub1": {database: "database1", collection: "users", filters: nil},
			"sub2": {database: "database1", collection: "orders", filters: nil},
		},
	}

	result := rs.reconnect()

	assert.True(t, result)
	// Verify subscriptions were sent
	assert.Len(t, newMockStream.sentMsgs, 2)
}

// TestRemoteStream_ResubscribeAll_Empty tests resubscribeAll with no subscriptions.
func TestRemoteStream_ResubscribeAll_Empty(t *testing.T) {
	rs := &remoteStream{
		logger:              slog.Default(),
		activeSubscriptions: make(map[string]*subscriptionInfo),
	}

	err := rs.resubscribeAll()
	assert.NoError(t, err)
}

// TestRemoteStream_ResubscribeAll_Success tests resubscribeAll with subscriptions.
func TestRemoteStream_ResubscribeAll_Success(t *testing.T) {
	t.Parallel()
	mockStream := &mockGRPCStreamClient{}

	rs := &remoteStream{
		grpcStream: mockStream,
		logger:     slog.Default(),
		activeSubscriptions: map[string]*subscriptionInfo{
			"sub1": {database: "database1", collection: "users", filters: nil},
		},
	}

	err := rs.resubscribeAll()
	assert.NoError(t, err)
	assert.Len(t, mockStream.sentMsgs, 1)

	// Verify the sent message
	msg := mockStream.sentMsgs[0]
	subscribe := msg.GetSubscribe()
	require.NotNil(t, subscribe)
	assert.Equal(t, "sub1", subscribe.SubscriptionId)
	assert.Equal(t, "database1", subscribe.Database)
	assert.Equal(t, "users", subscribe.Collection)
}

// TestRemoteStream_ResubscribeAll_SendError tests resubscribeAll when send fails.
func TestRemoteStream_ResubscribeAll_SendError(t *testing.T) {
	mockStream := &mockGRPCStreamClient{
		sendErr: errors.New("send failed"),
	}

	rs := &remoteStream{
		grpcStream: mockStream,
		logger:     slog.Default(),
		activeSubscriptions: map[string]*subscriptionInfo{
			"sub1": {database: "database1", collection: "users", filters: nil},
		},
	}

	err := rs.resubscribeAll()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to resubscribe")
}

// TestRemoteStream_HeartbeatLoop_Exit tests heartbeat loop exits under various conditions.
func TestRemoteStream_HeartbeatLoop_Exit(t *testing.T) {
	t.Parallel()

	t.Run("ContextDone", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(context.Background())

		client := &streamerClient{
			config: ClientConfig{HeartbeatInterval: 10 * time.Millisecond},
		}

		rs := &remoteStream{
			ctx:           ctx,
			logger:        slog.Default(),
			client:        client,
			heartbeatStop: make(chan struct{}),
		}

		done := make(chan struct{})
		go func() {
			rs.heartbeatLoop()
			close(done)
		}()

		cancel()

		select {
		case <-done:
		case <-time.After(100 * time.Millisecond):
			t.Fatal("heartbeatLoop should have exited on context cancel")
		}
	})

	t.Run("StopChannel", func(t *testing.T) {
		t.Parallel()
		client := &streamerClient{
			config: ClientConfig{HeartbeatInterval: 10 * time.Millisecond},
		}

		stopChan := make(chan struct{})
		rs := &remoteStream{
			ctx:           context.Background(),
			logger:        slog.Default(),
			client:        client,
			heartbeatStop: stopChan,
		}

		done := make(chan struct{})
		go func() {
			rs.heartbeatLoop()
			close(done)
		}()

		close(stopChan)

		select {
		case <-done:
		case <-time.After(100 * time.Millisecond):
			t.Fatal("heartbeatLoop should have exited on stop channel close")
		}
	})

	t.Run("Closed", func(t *testing.T) {
		t.Parallel()
		client := &streamerClient{
			config: ClientConfig{HeartbeatInterval: 10 * time.Millisecond},
		}

		rs := &remoteStream{
			ctx:           context.Background(),
			grpcStream:    &mockGRPCStreamClient{},
			logger:        slog.Default(),
			client:        client,
			state:         StateConnected,
			closed:        true,
			heartbeatStop: make(chan struct{}),
		}

		done := make(chan struct{})
		go func() {
			rs.heartbeatLoop()
			close(done)
		}()

		select {
		case <-done:
		case <-time.After(50 * time.Millisecond):
			t.Fatal("heartbeatLoop should have exited when closed")
		}
	})
}

// TestRemoteStream_HeartbeatLoop_SendsHeartbeat tests heartbeat is sent when connected.
func TestRemoteStream_HeartbeatLoop_SendsHeartbeat(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mockStream := &mockGRPCStreamClient{}

	client := &streamerClient{
		config: ClientConfig{
			HeartbeatInterval: 10 * time.Millisecond,
		},
	}

	rs := &remoteStream{
		ctx:           ctx,
		grpcStream:    mockStream,
		logger:        slog.Default(),
		client:        client,
		state:         StateConnected,
		heartbeatStop: make(chan struct{}),
	}

	done := make(chan struct{})
	go func() {
		rs.heartbeatLoop()
		close(done)
	}()

	// Wait for at least one heartbeat
	time.Sleep(25 * time.Millisecond)
	cancel()

	<-done

	// Should have sent at least one heartbeat
	mockStream.mu.Lock()
	sentCount := len(mockStream.sentMsgs)
	mockStream.mu.Unlock()

	assert.GreaterOrEqual(t, sentCount, 1)
}

// TestRemoteStream_HeartbeatLoop_SkipsWhenNotConnected tests heartbeat is skipped when not connected.
func TestRemoteStream_HeartbeatLoop_SkipsWhenNotConnected(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mockStream := &mockGRPCStreamClient{}

	client := &streamerClient{
		config: ClientConfig{
			HeartbeatInterval: 10 * time.Millisecond,
		},
	}

	rs := &remoteStream{
		ctx:           ctx,
		grpcStream:    mockStream,
		logger:        slog.Default(),
		client:        client,
		state:         StateReconnecting, // Not connected
		heartbeatStop: make(chan struct{}),
	}

	done := make(chan struct{})
	go func() {
		rs.heartbeatLoop()
		close(done)
	}()

	// Wait a bit
	time.Sleep(25 * time.Millisecond)
	cancel()

	<-done

	// Should not have sent any heartbeats
	mockStream.mu.Lock()
	sentCount := len(mockStream.sentMsgs)
	mockStream.mu.Unlock()

	assert.Equal(t, 0, sentCount)
}

// TestRemoteStream_ActivityMonitor_Exit tests activity monitor exits under various conditions.
func TestRemoteStream_ActivityMonitor_Exit(t *testing.T) {
	t.Parallel()

	t.Run("ContextDone", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(context.Background())

		client := &streamerClient{
			config: ClientConfig{ActivityTimeout: 30 * time.Millisecond},
		}

		rs := &remoteStream{
			ctx:           ctx,
			logger:        slog.Default(),
			client:        client,
			heartbeatStop: make(chan struct{}),
		}

		done := make(chan struct{})
		go func() {
			rs.activityMonitor()
			close(done)
		}()

		cancel()

		select {
		case <-done:
		case <-time.After(100 * time.Millisecond):
			t.Fatal("activityMonitor should have exited on context cancel")
		}
	})

	t.Run("StopChannel", func(t *testing.T) {
		t.Parallel()
		client := &streamerClient{
			config: ClientConfig{ActivityTimeout: 30 * time.Millisecond},
		}

		stopChan := make(chan struct{})
		rs := &remoteStream{
			ctx:           context.Background(),
			logger:        slog.Default(),
			client:        client,
			heartbeatStop: stopChan,
		}

		done := make(chan struct{})
		go func() {
			rs.activityMonitor()
			close(done)
		}()

		close(stopChan)

		select {
		case <-done:
		case <-time.After(100 * time.Millisecond):
			t.Fatal("activityMonitor should have exited on stop channel close")
		}
	})
}

// TestRemoteStream_ActivityMonitor_DetectsStale tests activity monitor detects stale connection.
func TestRemoteStream_ActivityMonitor_DetectsStale(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mockStream := &mockGRPCStreamClient{}

	// Use 200 milliseconds timeout -> checkInterval = 200ms (the minimum)
	client := &streamerClient{
		config: ClientConfig{
			ActivityTimeout: 200 * time.Millisecond,
		},
	}

	rs := &remoteStream{
		ctx:             ctx,
		grpcStream:      mockStream,
		logger:          slog.Default(),
		client:          client,
		state:           StateConnected,
		lastMessageTime: time.Now().Add(-10 * time.Second), // Well past timeout
		heartbeatStop:   make(chan struct{}),
	}

	done := make(chan struct{})
	go func() {
		rs.activityMonitor()
		close(done)
	}()

	// Wait for activity monitor to detect staleness and close stream
	// checkInterval is 70ms, so wait ~100ms for at least one check
	time.Sleep(100 * time.Millisecond)
	cancel()

	<-done

	// Should have closed the stream
	mockStream.mu.Lock()
	closed := mockStream.closedSend
	mockStream.mu.Unlock()

	assert.True(t, closed)
}

// TestRemoteStream_ActivityMonitor_SkipsWhenNotConnected tests activity monitor skips when not connected.
func TestRemoteStream_ActivityMonitor_SkipsWhenNotConnected(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mockStream := &mockGRPCStreamClient{}

	client := &streamerClient{
		config: ClientConfig{
			ActivityTimeout: 10 * time.Millisecond,
		},
	}

	rs := &remoteStream{
		ctx:             ctx,
		grpcStream:      mockStream,
		logger:          slog.Default(),
		client:          client,
		state:           StateReconnecting, // Not connected
		lastMessageTime: time.Now().Add(-1 * time.Second),
		heartbeatStop:   make(chan struct{}),
	}

	done := make(chan struct{})
	go func() {
		rs.activityMonitor()
		close(done)
	}()

	// Wait a bit
	time.Sleep(30 * time.Millisecond)
	cancel()

	<-done

	// Should NOT have closed the stream because not connected
	mockStream.mu.Lock()
	closed := mockStream.closedSend
	mockStream.mu.Unlock()

	assert.False(t, closed)
}

// TestRemoteStream_HeartbeatLoop_SendError tests heartbeat loop handles send error gracefully.
func TestRemoteStream_HeartbeatLoop_SendError(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mockStream := &mockGRPCStreamClient{
		sendErr: errors.New("send failed"),
	}

	client := &streamerClient{
		config: ClientConfig{
			HeartbeatInterval: 10 * time.Millisecond,
		},
	}

	rs := &remoteStream{
		ctx:           ctx,
		grpcStream:    mockStream,
		logger:        slog.Default(),
		client:        client,
		state:         StateConnected,
		heartbeatStop: make(chan struct{}),
	}

	done := make(chan struct{})
	go func() {
		rs.heartbeatLoop()
		close(done)
	}()

	// Wait for heartbeat attempt
	time.Sleep(25 * time.Millisecond)
	cancel()

	select {
	case <-done:
		// Success - loop continued despite error
	case <-time.After(100 * time.Millisecond):
		t.Fatal("heartbeatLoop should have exited")
	}
}

// TestRemoteStream_Subscribe_StoresSubscriptionInfo tests that successful subscribe stores subscription info.
func TestRemoteStream_Subscribe_StoresSubscriptionInfo(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Create a mock that will respond to any subscribe request
	mockStream := &mockAutoRespondStream{}

	rs := &remoteStream{
		ctx:                 ctx,
		grpcStream:          mockStream,
		logger:              slog.Default(),
		client:              nil,
		pendingSubscribes:   make(map[string]chan *pb.SubscribeResponse),
		activeSubscriptions: make(map[string]*subscriptionInfo),
	}

	// Start a goroutine that watches for pending subscribes and responds
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Millisecond):
				rs.pendingSubscribesMu.Lock()
				for subID, ch := range rs.pendingSubscribes {
					select {
					case ch <- &pb.SubscribeResponse{
						SubscriptionId: subID,
						Success:        true,
					}:
					default:
					}
				}
				rs.pendingSubscribesMu.Unlock()
			}
		}
	}()

	filters := []model.Filter{{Field: "status", Op: model.OpEq, Value: "active"}}
	subID, err := rs.Subscribe("database1", "users", filters)

	require.NoError(t, err)
	assert.NotEmpty(t, subID)

	// Verify subscription info was stored
	rs.activeSubscriptionsMu.Lock()
	info, exists := rs.activeSubscriptions[subID]
	rs.activeSubscriptionsMu.Unlock()

	assert.True(t, exists, "subscription info should be stored")
	if exists {
		assert.Equal(t, "database1", info.database)
		assert.Equal(t, "users", info.collection)
		assert.Len(t, info.filters, 1)
	}
}

// mockAutoRespondStream is a mock that doesn't block on Recv (returns EOF immediately)
type mockAutoRespondStream struct {
	sentMsgs   []*pb.GatewayMessage
	mu         sync.Mutex
	closedSend bool
}

func (m *mockAutoRespondStream) Send(msg *pb.GatewayMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sentMsgs = append(m.sentMsgs, msg)
	return nil
}

func (m *mockAutoRespondStream) Recv() (*pb.StreamerMessage, error) {
	// Block forever to prevent recv loop from running
	select {}
}

func (m *mockAutoRespondStream) CloseSend() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closedSend = true
	return nil
}

func (m *mockAutoRespondStream) Header() (metadata.MD, error) { return nil, nil }
func (m *mockAutoRespondStream) Trailer() metadata.MD         { return nil }
func (m *mockAutoRespondStream) Context() context.Context     { return context.Background() }
func (m *mockAutoRespondStream) SendMsg(interface{}) error    { return nil }
func (m *mockAutoRespondStream) RecvMsg(interface{}) error    { return nil }

// mockDynamicServiceClient allows dynamic Stream behavior for testing reconnect scenarios.
type mockDynamicServiceClient struct {
	pb.StreamerServiceClient
	streamFn func() (pb.StreamerService_StreamClient, error)
}

func (m *mockDynamicServiceClient) Stream(ctx context.Context, opts ...grpc.CallOption) (pb.StreamerService_StreamClient, error) {
	return m.streamFn()
}

// TestRemoteStream_Reconnect_ResubscribeFails tests reconnect handles resubscribe failure.
func TestRemoteStream_Reconnect_ResubscribeFails(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	attemptCount := 0
	var mu sync.Mutex

	// First stream will fail resubscribe, second will succeed
	mockServiceClient := &mockDynamicServiceClient{
		streamFn: func() (pb.StreamerService_StreamClient, error) {
			mu.Lock()
			attemptCount++
			count := attemptCount
			mu.Unlock()

			if count == 1 {
				// First attempt: return a stream that will fail on Send (for resubscribe)
				return &mockGRPCStreamClient{
					sendErr: errors.New("resubscribe send failed"),
				}, nil
			}
			// Second attempt: return a working stream
			return &mockGRPCStreamClient{}, nil
		},
	}

	client := &streamerClient{
		config: ClientConfig{
			InitialBackoff:    1 * time.Millisecond,
			MaxBackoff:        5 * time.Millisecond,
			BackoffMultiplier: 1.5,
			MaxRetries:        0,
		},
		client: mockServiceClient,
	}

	rs := &remoteStream{
		ctx:    ctx,
		logger: slog.Default(),
		client: client,
		activeSubscriptions: map[string]*subscriptionInfo{
			"sub1": {database: "database1", collection: "users", filters: nil},
		},
	}

	result := rs.reconnect()

	assert.True(t, result)
	mu.Lock()
	finalCount := attemptCount
	mu.Unlock()
	assert.Equal(t, 2, finalCount) // Should have tried twice
}

func TestRemoteStream_TerminalIngestionFailureDoesNotReconnect(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	failure := status.Error(codes.FailedPrecondition, "Puller history expired")
	transport := &mockGRPCStreamClient{recvErr: failure}
	endpoint := &mockStreamerServiceClient{streamClient: transport}
	client := &streamerClient{
		config: DefaultClientConfig(),
		client: endpoint,
		logger: slog.Default(),
	}
	stream, err := client.Stream(ctx)
	require.NoError(t, err)
	_, err = stream.Recv()
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
	require.ErrorContains(t, err, "Puller history expired")
	assert.Equal(t, 1, endpoint.streamCalls)
	transport.mu.Lock()
	assert.True(t, transport.closedSend)
	transport.mu.Unlock()
	_, err = stream.Subscribe("database1", "users", nil)
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
	rs := stream.(*remoteStream)
	waitSignal(t, rs.heartbeatStop)
	assert.Equal(t, StateDisconnected, rs.State())
}

type terminalMockGRPCStreamClient struct {
	mockGRPCStreamClient
	ctx     context.Context
	failure chan error
}

func (m *terminalMockGRPCStreamClient) Recv() (*pb.StreamerMessage, error) {
	select {
	case err := <-m.failure:
		return nil, err
	case <-m.ctx.Done():
		return nil, m.ctx.Err()
	}
}

func TestRemoteStream_TerminalFailureUnblocksPendingSubscribe(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	transport := &terminalMockGRPCStreamClient{ctx: ctx, failure: make(chan error, 1)}
	endpoint := &mockStreamerServiceClient{streamClient: transport}
	client := &streamerClient{config: DefaultClientConfig(), client: endpoint, logger: slog.Default()}
	stream, err := client.Stream(ctx)
	require.NoError(t, err)
	result := make(chan error, 1)
	go func() {
		_, err := stream.Subscribe("database1", "users", nil)
		result <- err
	}()
	require.Eventually(t, func() bool {
		transport.mu.Lock()
		defer transport.mu.Unlock()
		return len(transport.sentMsgs) == 1
	}, time.Second, time.Millisecond)
	transport.failure <- status.Error(codes.FailedPrecondition, "ingestion failed")
	select {
	case err := <-result:
		require.Equal(t, codes.FailedPrecondition, status.Code(err))
	case <-ctx.Done():
		t.Fatal("subscription request remained blocked after ingestion failure")
	}
	_, err = stream.Recv()
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
	assert.Equal(t, 1, endpoint.streamCalls)
}
