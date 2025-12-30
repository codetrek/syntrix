package client

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	pullerv1 "github.com/codetrek/syntrix/api/puller/v1"
	"google.golang.org/grpc"
)

// mockSubscribeClient implements pullerv1.PullerService_SubscribeClient
type mockSubscribeClient struct {
	grpc.ClientStream
	events []*pullerv1.Event
	index  int
}

func (m *mockSubscribeClient) Recv() (*pullerv1.Event, error) {
	if m.index >= len(m.events) {
		return nil, io.EOF
	}
	evt := m.events[m.index]
	m.index++
	return evt, nil
}

// mockPullerServiceClient implements pullerv1.PullerServiceClient
type mockPullerServiceClient struct {
	subscribeFunc func(ctx context.Context, in *pullerv1.SubscribeRequest, opts ...grpc.CallOption) (pullerv1.PullerService_SubscribeClient, error)
}

func (m *mockPullerServiceClient) Subscribe(ctx context.Context, in *pullerv1.SubscribeRequest, opts ...grpc.CallOption) (pullerv1.PullerService_SubscribeClient, error) {
	return m.subscribeFunc(ctx, in, opts...)
}

func TestClient_Subscribe(t *testing.T) {
	t.Parallel()
	events := []*pullerv1.Event{
		{
			Id:            "evt-1",
			Tenant:        "tenant-1",
			Collection:    "users",
			DocumentId:    "doc-1",
			OperationType: "insert",
			Timestamp:     1234567890,
		},
		{
			Id:            "evt-2",
			Tenant:        "tenant-1",
			Collection:    "users",
			DocumentId:    "doc-2",
			OperationType: "update",
			Timestamp:     1234567891,
		},
	}

	mockClient := &mockPullerServiceClient{
		subscribeFunc: func(ctx context.Context, in *pullerv1.SubscribeRequest, opts ...grpc.CallOption) (pullerv1.PullerService_SubscribeClient, error) {
			return &mockSubscribeClient{events: events}, nil
		},
	}

	c := &Client{
		client: mockClient,
		logger: slog.Default(),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ch, err := c.Subscribe(ctx, "consumer-1", "")
	if err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}

	// Read events
	count := 0
	for i := 0; i < 2; i++ {
		select {
		case evt := <-ch:
			if evt == nil {
				t.Fatal("Received nil event")
			}
			count++
		case <-ctx.Done():
			t.Fatal("Timeout waiting for events")
		}
	}

	if count != 2 {
		t.Errorf("Expected 2 events, got %d", count)
	}
}

func TestClient_SubscribeWithCoalesce(t *testing.T) {
	t.Parallel()
	events := []*pullerv1.Event{
		{Id: "evt-1"},
	}

	mockClient := &mockPullerServiceClient{
		subscribeFunc: func(ctx context.Context, in *pullerv1.SubscribeRequest, opts ...grpc.CallOption) (pullerv1.PullerService_SubscribeClient, error) {
			if !in.CoalesceOnCatchUp {
				t.Error("Expected CoalesceOnCatchUp to be true")
			}
			return &mockSubscribeClient{events: events}, nil
		},
	}

	c := &Client{
		client: mockClient,
		logger: slog.Default(),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ch, err := c.SubscribeWithCoalesce(ctx, "consumer-1", "")
	if err != nil {
		t.Fatalf("SubscribeWithCoalesce failed: %v", err)
	}

	select {
	case evt := <-ch:
		if evt == nil {
			t.Fatal("Received nil event")
		}
	case <-ctx.Done():
		t.Fatal("Timeout waiting for events")
	}
}

func TestClient_Close(t *testing.T) {
	t.Parallel()
	// Test Close with nil conn
	c := &Client{}
	if err := c.Close(); err != nil {
		t.Errorf("Close with nil conn failed: %v", err)
	}
}
