package grpc

import (
	"context"
	"errors"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	pullerv1 "github.com/syntrixbase/syntrix/api/gen/puller/v1"
	"github.com/syntrixbase/syntrix/internal/puller/config"
	"github.com/syntrixbase/syntrix/internal/puller/events"
	grpcapi "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type sourceFunc func(context.Context, events.SubscribeOptions) (events.Subscription, error)

func (f sourceFunc) Subscribe(ctx context.Context, opts events.SubscribeOptions) (events.Subscription, error) {
	return f(ctx, opts)
}

type subscriptionFixture struct {
	initialProgress string
	initialized     bool
	next            func(context.Context) (*events.PullerEvent, error)
	closed          atomic.Int32
}

func (s *subscriptionFixture) Next(ctx context.Context) (*events.PullerEvent, error) {
	if !s.initialized {
		s.initialized = true
		progress := s.initialProgress
		if progress == "" {
			progress = "start"
		}
		return &events.PullerEvent{Progress: progress}, nil
	}
	return s.next(ctx)
}
func (s *subscriptionFixture) Close() error { s.closed.Add(1); return nil }

type streamFixture struct {
	grpcapi.ServerStream
	ctx    context.Context
	send   func(*pullerv1.PullerEvent) error
	header func(metadata.MD) error
}

func (s *streamFixture) Context() context.Context             { return s.ctx }
func (s *streamFixture) Send(evt *pullerv1.PullerEvent) error { return s.send(evt) }
func (s *streamFixture) SendHeader(md metadata.MD) error {
	if s.header != nil {
		return s.header(md)
	}
	return nil
}

func TestServerPreservesSharedSubscriptionOrderAndProgress(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	want := []*events.PullerEvent{
		{Change: &events.StoreChangeEvent{EventID: "replayed", Backend: "mongo"}, Progress: "p1"},
		{Progress: "coalesced-window-end"},
		{Change: &events.StoreChangeEvent{EventID: "live", Backend: "mongo"}, Progress: "p3"},
	}
	index := 0
	sub := &subscriptionFixture{initialProgress: "cursor", next: func(context.Context) (*events.PullerEvent, error) {
		if index == len(want) {
			return nil, io.EOF
		}
		evt := want[index]
		index++
		return evt, nil
	}}
	registered := false
	source := sourceFunc(func(_ context.Context, opts events.SubscribeOptions) (events.Subscription, error) {
		assert.Equal(t, events.SubscribeOptions{ConsumerID: "label", After: "cursor", CoalesceOnCatchUp: true}, opts)
		registered = true
		return sub, nil
	})
	server := NewServer(config.DefaultConfig().GRPC, source, nil)
	defer server.Shutdown()
	var got []*pullerv1.PullerEvent
	stream := &streamFixture{ctx: ctx, header: func(md metadata.MD) error {
		assert.True(t, registered)
		assert.Equal(t, []string{"ready"}, md.Get("syntrix-puller-subscription"))
		return nil
	}, send: func(evt *pullerv1.PullerEvent) error { got = append(got, evt); return nil }}
	require.NoError(t, server.Subscribe(&pullerv1.SubscribeRequest{ConsumerId: "label", After: "cursor", CoalesceOnCatchUp: true}, stream))
	require.Len(t, got, 4)
	assert.Nil(t, got[0].ChangeEvent)
	assert.Equal(t, "cursor", got[0].Progress)
	got = got[1:]
	assert.Equal(t, "replayed", got[0].ChangeEvent.EventId)
	assert.Nil(t, got[1].ChangeEvent)
	assert.Equal(t, "coalesced-window-end", got[1].Progress)
	assert.Equal(t, "live", got[2].ChangeEvent.EventId)
	assert.EqualValues(t, 1, sub.closed.Load())
	assert.Zero(t, server.SubscriberCount())
}

func TestServerPropagatesSendFailureAndClosesSubscription(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	sub := &subscriptionFixture{next: func(ctx context.Context) (*events.PullerEvent, error) {
		return &events.PullerEvent{Progress: "p1"}, nil
	}}
	server := NewServer(config.DefaultConfig().GRPC, sourceFunc(func(context.Context, events.SubscribeOptions) (events.Subscription, error) { return sub, nil }), nil)
	defer server.Shutdown()
	sendErr := errors.New("write failed")
	err := server.Subscribe(&pullerv1.SubscribeRequest{}, &streamFixture{ctx: ctx, send: func(*pullerv1.PullerEvent) error { return sendErr }})
	require.ErrorContains(t, err, sendErr.Error())
	assert.Equal(t, codes.Internal, status.Code(err))
	assert.EqualValues(t, 1, sub.closed.Load())
	assert.Zero(t, server.SubscriberCount())
}

func TestServerConnectionLimitAndShutdown(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	entered := make(chan struct{})
	sub := &subscriptionFixture{next: func(ctx context.Context) (*events.PullerEvent, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	cfg := config.DefaultConfig().GRPC
	cfg.MaxConnections = 1
	server := NewServer(cfg, sourceFunc(func(context.Context, events.SubscribeOptions) (events.Subscription, error) { return sub, nil }), nil)
	defer server.Shutdown()
	done := make(chan error, 1)
	go func() {
		done <- server.Subscribe(&pullerv1.SubscribeRequest{}, &streamFixture{ctx: ctx, header: func(metadata.MD) error { close(entered); return nil }, send: func(*pullerv1.PullerEvent) error { return nil }})
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	assert.Equal(t, 1, server.SubscriberCount())
	err := server.Subscribe(&pullerv1.SubscribeRequest{}, &streamFixture{ctx: ctx})
	assert.Equal(t, codes.ResourceExhausted, status.Code(err))
	server.Shutdown()
	select {
	case err := <-done:
		assert.Equal(t, codes.Unavailable, status.Code(err))
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	assert.Zero(t, server.SubscriberCount())
	assert.EqualValues(t, 1, sub.closed.Load())
	assert.Equal(t, codes.Unavailable, status.Code(server.Subscribe(&pullerv1.SubscribeRequest{}, &streamFixture{ctx: ctx})))
}
