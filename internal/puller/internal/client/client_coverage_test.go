package client

import (
	"context"
	"fmt"
	"log/slog"
	"testing"
	"time"

	pullerv1 "github.com/codetrek/syntrix/api/puller/v1"
	"google.golang.org/grpc"
)

// mockErrorSubscribeClient implements pullerv1.PullerService_SubscribeClient that returns an error
type mockErrorSubscribeClient struct {
	grpc.ClientStream
	err error
}

func (m *mockErrorSubscribeClient) Recv() (*pullerv1.PullerEvent, error) {
	return nil, m.err
}

func TestNew(t *testing.T) {
	t.Parallel()
	// Test with valid address (doesn't need to be reachable for NewClient)
	c, err := New("localhost:50051", nil)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	if c == nil {
		t.Fatal("New() returned nil client")
	}
	if c.logger == nil {
		t.Error("New() should initialize logger")
	}

	// Clean up
	_ = c.Close()
}

func TestClient_Close_Real(t *testing.T) {
	t.Parallel()
	c, err := New("localhost:50051", slog.Default())
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}

	err = c.Close()
	if err != nil {
		t.Errorf("Close() failed: %v", err)
	}
}

func TestClient_Subscribe_ConnectionError(t *testing.T) {
	t.Parallel()
	// Reuse mockPullerServiceClient from client_grpc_test.go
	mockClient := &mockPullerServiceClient{
		subscribeFunc: func(ctx context.Context, in *pullerv1.SubscribeRequest, opts ...grpc.CallOption) (pullerv1.PullerService_SubscribeClient, error) {
			return nil, fmt.Errorf("connection failed")
		},
	}

	c := &Client{
		client: mockClient,
		logger: slog.Default(),
	}

	_, err := c.Subscribe(context.Background(), "c1", "")
	if err == nil {
		t.Error("Expected error from Subscribe")
	}
}

func TestClient_Subscribe_StreamError(t *testing.T) {
	t.Parallel()
	mockStream := &mockErrorSubscribeClient{
		err: fmt.Errorf("stream broken"),
	}

	mockClient := &mockPullerServiceClient{
		subscribeFunc: func(ctx context.Context, in *pullerv1.SubscribeRequest, opts ...grpc.CallOption) (pullerv1.PullerService_SubscribeClient, error) {
			return mockStream, nil
		},
	}

	c := &Client{
		client: mockClient,
		logger: slog.Default(),
	}

	ch, err := c.Subscribe(context.Background(), "c1", "")
	if err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}

	// Should receive no events and channel should close (or log error and return)
	select {
	case _, ok := <-ch:
		if ok {
			t.Error("Channel should be closed without events")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for channel close")
	}
}

func TestClient_SubscribeWithCoalesce_Error(t *testing.T) {
	t.Parallel()
	mockClient := &mockPullerServiceClient{
		subscribeFunc: func(ctx context.Context, in *pullerv1.SubscribeRequest, opts ...grpc.CallOption) (pullerv1.PullerService_SubscribeClient, error) {
			return nil, fmt.Errorf("connection failed")
		},
	}

	c := &Client{
		client: mockClient,
		logger: slog.Default(),
	}

	_, err := c.SubscribeWithCoalesce(context.Background(), "c1", "")
	if err == nil {
		t.Error("Expected error from SubscribeWithCoalesce")
	}
}
