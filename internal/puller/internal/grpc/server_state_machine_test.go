package grpc

import (
	"context"
	"sync"
	"testing"
	"time"

	pullerv1 "github.com/codetrek/syntrix/api/puller/v1"
	"github.com/codetrek/syntrix/internal/config"
	"github.com/codetrek/syntrix/internal/puller/events"
	"github.com/stretchr/testify/mock"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// --- Mocks ---

type mockStream struct {
	mock.Mock
	grpc.ServerStream
	ctx context.Context
	t   *testing.T
}

func (m *mockStream) Context() context.Context {
	if m.ctx != nil {
		return m.ctx
	}
	return context.Background()
}

func (m *mockStream) Send(event *pullerv1.Event) error {
	if m.t != nil {
		m.t.Logf("MockStream.Send called with event: %s", event.Id)
	}
	args := m.Called(event)
	return args.Error(0)
}

func (m *mockStream) SetHeader(md metadata.MD) error {
	return nil
}

func (m *mockStream) SendHeader(md metadata.MD) error {
	return nil
}

func (m *mockStream) SetTrailer(md metadata.MD) {
}

type controllableIterator struct {
	events []*events.NormalizedEvent
	idx    int
}

func (m *controllableIterator) Next() bool {
	m.idx++
	return m.idx <= len(m.events)
}

func (m *controllableIterator) Event() *events.NormalizedEvent {
	if m.idx > 0 && m.idx <= len(m.events) {
		return m.events[m.idx-1]
	}
	return nil
}

func (m *controllableIterator) Err() error {
	return nil
}

func (m *controllableIterator) Close() error {
	return nil
}

type controllableEventSource struct {
	replayFunc func(ctx context.Context, after map[string]string, coalesce bool) (events.Iterator, error)
	handler    func(ctx context.Context, backendName string, event *events.NormalizedEvent) error
}

func (m *controllableEventSource) SetEventHandler(handler func(ctx context.Context, backendName string, event *events.NormalizedEvent) error) {
	m.handler = handler
}

func (m *controllableEventSource) EmitEvent(ctx context.Context, backendName string, event *events.NormalizedEvent) error {
	if m.handler != nil {
		return m.handler(ctx, backendName, event)
	}
	return nil
}

func (m *controllableEventSource) Replay(ctx context.Context, after map[string]string, coalesce bool) (events.Iterator, error) {
	if m.replayFunc != nil {
		return m.replayFunc(ctx, after, coalesce)
	}
	return &controllableIterator{events: nil}, nil
}

// --- Tests ---

func TestServer_StateMachine_HappyPath(t *testing.T) {
	// Scenario: Replay finishes -> Switch to Live -> Receive events

	// Setup
	cfg := config.PullerGRPCConfig{ChannelSize: 100}
	replayEvt := &events.NormalizedEvent{
		EventID:     "replay-1",
		ClusterTime: events.ClusterTime{T: 100, I: 1},
	}

	source := &controllableEventSource{
		replayFunc: func(ctx context.Context, after map[string]string, coalesce bool) (events.Iterator, error) {
			return &controllableIterator{events: []*events.NormalizedEvent{replayEvt}}, nil
		},
	}
	server := NewServer(cfg, source, nil)

	// Start the server to initialize the event loop
	server.Start(context.Background())
	defer server.Stop(context.Background())

	// Mock Stream
	stream := &mockStream{ctx: context.Background(), t: t}

	// Expectation:
	// 1. Send replay event
	// 2. Send live event

	done := make(chan struct{})

	stream.On("Send", mock.MatchedBy(func(event *pullerv1.Event) bool {
		// Check if it's the replay event
		if event.Id == "replay-1" {
			return true
		}
		return false
	})).Return(nil).Once()

	stream.On("Send", mock.MatchedBy(func(event *pullerv1.Event) bool {
		// Check if it's the live event
		if event.Id == "live-1" {
			close(done) // Signal completion
			return true
		}
		return false
	})).Return(nil).Once()

	// Run Subscribe in a goroutine
	go func() {
		req := &pullerv1.SubscribeRequest{
			ConsumerId: "test-consumer",
		}
		server.Subscribe(req, stream)
	}()

	// Wait for replay to finish (it happens immediately in this mock)
	// We need to wait a bit for the server to switch to live mode and register the subscriber
	time.Sleep(100 * time.Millisecond)

	// Broadcast a live event via source
	liveEvt := &events.NormalizedEvent{
		EventID:     "live-1",
		ClusterTime: events.ClusterTime{T: 200, I: 1},
	}
	source.EmitEvent(context.Background(), "db1", liveEvt)

	// Wait for test completion
	select {
	case <-done:
	// Success
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for live event")
	}

	stream.AssertExpectations(t)
}

func TestServer_StateMachine_LiveDowngrade(t *testing.T) {
	// Scenario: Live mode -> Channel overflow -> Switch to Catch-up -> Replay -> Switch back to Live

	// Setup with small channel size to force overflow
	cfg := config.PullerGRPCConfig{ChannelSize: 1}

	var replayCount int
	var mu sync.Mutex

	replayEvt2 := &events.NormalizedEvent{
		EventID:     "replay-2",
		ClusterTime: events.ClusterTime{T: 103, I: 1}, // Newer than overflow events
	}

	source := &controllableEventSource{
		replayFunc: func(ctx context.Context, after map[string]string, coalesce bool) (events.Iterator, error) {
			mu.Lock()
			defer mu.Unlock()
			replayCount++
			t.Logf("Replay called. Count: %d", replayCount)
			if replayCount == 1 {
				// First replay: empty
				return &controllableIterator{events: []*events.NormalizedEvent{}}, nil
			}
			// Second replay (after overflow): return some events
			return &controllableIterator{events: []*events.NormalizedEvent{replayEvt2}}, nil
		},
	}
	server := NewServer(cfg, source, nil)

	// Start the server to initialize the event loop
	server.Start(context.Background())
	defer server.Stop(context.Background())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream := &mockStream{ctx: ctx, t: t}

	// 1. Initial connection (empty replay -> live)

	// Run Subscribe
	go func() {
		req := &pullerv1.SubscribeRequest{
			ConsumerId: "overflow-consumer",
		}
		server.Subscribe(req, stream)
	}()

	time.Sleep(100 * time.Millisecond) // Wait for live mode

	// 2. Fill the channel (size 1)
	evt1 := &events.NormalizedEvent{EventID: "evt-1", ClusterTime: events.ClusterTime{T: 100}}

	// Let's make stream.Send block for the first event
	sendBlock := make(chan struct{})
	stream.On("Send", mock.MatchedBy(func(event *pullerv1.Event) bool {
		return event.Id == "evt-1"
	})).Run(func(args mock.Arguments) {
		t.Log("Blocking Send(evt-1)")
		<-sendBlock // Block here
		t.Log("Unblocking Send(evt-1)")
	}).Return(nil).Once()

	// Broadcast evt-1. It will be picked up and stuck in Send.
	t.Log("Emitting evt-1")
	source.EmitEvent(context.Background(), "db1", evt1)
	time.Sleep(50 * time.Millisecond)

	// Now channel is empty (item picked up), but consumer is blocked.
	// Broadcast evt-2. It goes to channel (size 1). Channel full.
	t.Log("Emitting evt-2")
	evt2 := &events.NormalizedEvent{EventID: "evt-2", ClusterTime: events.ClusterTime{T: 101}}
	source.EmitEvent(context.Background(), "db1", evt2)
	time.Sleep(50 * time.Millisecond)

	// Broadcast evt-3. Channel full -> Overflow!
	t.Log("Emitting evt-3")
	evt3 := &events.NormalizedEvent{EventID: "evt-3", ClusterTime: events.ClusterTime{T: 102}}
	source.EmitEvent(context.Background(), "db1", evt3)
	time.Sleep(50 * time.Millisecond)

	// Expect evt-2 to be sent as well (it was in the channel)
	stream.On("Send", mock.MatchedBy(func(event *pullerv1.Event) bool {
		return event.Id == "evt-2"
	})).Return(nil).Once()

	// Expect the second replay event to be sent eventually
	replayDone := make(chan struct{})
	stream.On("Send", mock.MatchedBy(func(event *pullerv1.Event) bool {
		if event.Id == "replay-2" {
			close(replayDone)
			return true
		}
		return false
	})).Return(nil).Once()

	// Now unblock Send
	t.Log("Closing sendBlock")
	close(sendBlock)

	// The server should:
	// 1. Finish sending evt-1.
	// 2. Detect overflow (evt-3 caused it).
	// 3. Switch to Catch-up mode.
	// 4. Call Replay again (replayCount becomes 2).
	// 5. Send replay-2.

	select {
	case <-replayDone:
	// Success
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for replay event after overflow")
	}

	mu.Lock()
	if replayCount < 2 {
		t.Errorf("Expected at least 2 replays, got %d", replayCount)
	}
	mu.Unlock()
}

func TestServer_Boundary_EmptyReplay(t *testing.T) {
	// Scenario: Replay returns no events -> Immediate switch to Live

	cfg := config.PullerGRPCConfig{ChannelSize: 100}
	source := &controllableEventSource{
		replayFunc: func(ctx context.Context, after map[string]string, coalesce bool) (events.Iterator, error) {
			return &controllableIterator{events: []*events.NormalizedEvent{}}, nil
		},
	}
	server := NewServer(cfg, source, nil)
	server.Start(context.Background())
	defer server.Stop(context.Background())

	stream := &mockStream{ctx: context.Background(), t: t}

	done := make(chan struct{})
	stream.On("Send", mock.MatchedBy(func(event *pullerv1.Event) bool {
		if event.Id == "live-1" {
			close(done)
			return true
		}
		return false
	})).Return(nil).Once()

	go func() {
		req := &pullerv1.SubscribeRequest{ConsumerId: "empty-replay"}
		server.Subscribe(req, stream)
	}()

	time.Sleep(50 * time.Millisecond)

	// Emit live event
	liveEvt := &events.NormalizedEvent{EventID: "live-1", ClusterTime: events.ClusterTime{T: 200}}
	source.EmitEvent(context.Background(), "db1", liveEvt)

	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for live event")
	}
	stream.AssertExpectations(t)
}

func TestServer_Boundary_ImmediateCancel(t *testing.T) {
	// Scenario: Context cancelled before Replay starts

	cfg := config.PullerGRPCConfig{ChannelSize: 100}
	source := &controllableEventSource{}
	server := NewServer(cfg, source, nil)
	server.Start(context.Background())
	defer server.Stop(context.Background())

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	stream := &mockStream{ctx: ctx, t: t}

	req := &pullerv1.SubscribeRequest{ConsumerId: "cancel-consumer"}
	err := server.Subscribe(req, stream)

	// Subscribe returns nil on context cancellation (graceful disconnect)
	if err != nil {
		t.Errorf("Expected nil error on context cancellation, got: %v", err)
	}
}

func TestServer_Boundary_SendError(t *testing.T) {
	// Scenario: stream.Send returns error -> Should terminate subscription

	cfg := config.PullerGRPCConfig{ChannelSize: 100}
	replayEvt := &events.NormalizedEvent{EventID: "replay-1", ClusterTime: events.ClusterTime{T: 100}}

	source := &controllableEventSource{
		replayFunc: func(ctx context.Context, after map[string]string, coalesce bool) (events.Iterator, error) {
			return &controllableIterator{events: []*events.NormalizedEvent{replayEvt}}, nil
		},
	}
	server := NewServer(cfg, source, nil)
	server.Start(context.Background())
	defer server.Stop(context.Background())

	stream := &mockStream{ctx: context.Background(), t: t}

	// Send returns error
	stream.On("Send", mock.Anything).Return(context.Canceled).Once()

	req := &pullerv1.SubscribeRequest{ConsumerId: "error-consumer"}
	err := server.Subscribe(req, stream)

	if err == nil {
		t.Error("Expected error from Subscribe when Send fails")
	}
	stream.AssertExpectations(t)
}

func TestServer_Boundary_InvalidMarker(t *testing.T) {
	// Scenario: Subscribe with malformed after token

	cfg := config.PullerGRPCConfig{ChannelSize: 100}
	server := NewServer(cfg, &controllableEventSource{}, nil)

	stream := &mockStream{ctx: context.Background(), t: t}

	req := &pullerv1.SubscribeRequest{
		ConsumerId: "invalid-marker",
		After:      "invalid-base64-token",
	}

	err := server.Subscribe(req, stream)
	if err == nil {
		t.Error("Expected error for invalid marker")
	}
}
