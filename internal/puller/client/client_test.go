package client

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	pullerv1 "github.com/syntrixbase/syntrix/api/gen/puller/v1"
	"github.com/syntrixbase/syntrix/internal/core/storage"
	"github.com/syntrixbase/syntrix/internal/puller/config"
	"github.com/syntrixbase/syntrix/internal/puller/events"
	pullergrpc "github.com/syntrixbase/syntrix/internal/puller/grpc"
	grpcapi "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type localSource func(context.Context, events.SubscribeOptions) (events.Subscription, error)

func (f localSource) Subscribe(ctx context.Context, opts events.SubscribeOptions) (events.Subscription, error) {
	return f(ctx, opts)
}

type localSubscription struct {
	initialProgress string
	initialized     bool
	next            func(context.Context) (*events.PullerEvent, error)
	close           func() error
}

func (s *localSubscription) Next(ctx context.Context) (*events.PullerEvent, error) {
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
func (s *localSubscription) Close() error {
	if s.close != nil {
		return s.close()
	}
	return nil
}

func serveClient(t *testing.T, source pullergrpc.EventSource) (*Client, *pullergrpc.Server) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := grpcapi.NewServer()
	cfg := config.DefaultConfig().GRPC
	cfg.HeartbeatInterval = 0
	adapter := pullergrpc.NewServer(cfg, source, nil)
	pullerv1.RegisterPullerServiceServer(server, adapter)
	stopped := make(chan error, 1)
	go func() { stopped <- server.Serve(listener) }()
	client, err := NewWithConfig(listener.Addr().String(), nil, ClientConfig{InitialBackoff: time.Millisecond, MaxBackoff: 2 * time.Millisecond, MaxRetries: 2})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, client.Close())
		adapter.Shutdown()
		server.Stop()
		select {
		case err := <-stopped:
			require.NoError(t, err)
		case <-time.After(3 * time.Second):
			t.Error("server did not stop")
		}
	})
	return client, adapter
}

func TestGRPCMatchesLocalEnvelopesAndRegistersBeforeSubscribeReturns(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	txn := int64(7)
	want := []*events.PullerEvent{
		{Change: &events.StoreChangeEvent{
			EventID: "stable", Database: "database", MgoColl: "documents", MgoDocID: "doc", OpType: events.StoreOperationUpdate,
			FullDocument: &storage.StoredDoc{Id: "doc", Data: map[string]any{"value": "new"}},
			UpdateDesc:   &events.UpdateDescription{UpdatedFields: map[string]any{"value": "new"}, RemovedFields: []string{"old"}},
			ClusterTime:  events.ClusterTime{T: 10, I: 3}, TxnNumber: &txn, Timestamp: 123, Backend: "backend",
		}, Progress: "p1"},
		{Progress: "empty-coalesced-window"},
	}
	deliveries := make(chan *events.PullerEvent, 2)
	registered := atomic.Bool{}
	client, _ := serveClient(t, localSource(func(_ context.Context, opts events.SubscribeOptions) (events.Subscription, error) {
		assert.Equal(t, events.SubscribeOptions{ConsumerID: "parity", After: "checkpoint", CoalesceOnCatchUp: true}, opts)
		registered.Store(true)
		return &localSubscription{initialProgress: "checkpoint", next: func(ctx context.Context) (*events.PullerEvent, error) {
			select {
			case evt := <-deliveries:
				return evt, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}}, nil
	}))
	sub, err := client.Subscribe(ctx, events.SubscribeOptions{ConsumerID: "parity", After: "checkpoint", CoalesceOnCatchUp: true})
	require.NoError(t, err)
	defer sub.Close()
	assert.True(t, registered.Load())
	for _, evt := range want {
		deliveries <- evt
	}
	initial, err := sub.Next(ctx)
	require.NoError(t, err)
	assert.Equal(t, &events.PullerEvent{Progress: "checkpoint"}, initial)
	for _, expected := range want {
		actual, err := sub.Next(ctx)
		require.NoError(t, err)
		assert.Equal(t, expected, actual)
	}
}

func TestGRPCDomainErrorsRemainTerminalAtRegistrationAndDuringDelivery(t *testing.T) {
	t.Parallel()
	for _, startup := range []bool{true, false} {
		t.Run(map[bool]string{true: "registration", false: "delivery"}[startup], func(t *testing.T) {
			for _, code := range []events.ErrorCode{events.CodeInvalidCursor, events.CodeUnknownSource, events.CodeGenerationMismatch, events.CodeHistoryExpired, events.CodePositionAhead, events.CodeStorageFailure, events.CodeSourceUnavailable, events.CodeContinuityLost, events.CodeUnsupportedFormat, events.CodeOverloaded} {
				t.Run(string(code), func(t *testing.T) {
					ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
					defer cancel()
					failure := &events.Error{Code: code, Backend: "mongo", SourceID: "source", Generation: "g", DiscardedThrough: 3, CommittedThrough: 8, Cause: errors.New("private backend failure")}
					var registrations atomic.Int32
					client, _ := serveClient(t, localSource(func(context.Context, events.SubscribeOptions) (events.Subscription, error) {
						registrations.Add(1)
						if startup {
							return nil, failure
						}
						return &localSubscription{next: func(context.Context) (*events.PullerEvent, error) { return nil, failure }}, nil
					}))
					sub, err := client.Subscribe(ctx, events.SubscribeOptions{})
					if startup {
						require.Nil(t, sub)
					} else {
						require.NoError(t, err)
						initial, initialErr := sub.Next(ctx)
						require.NoError(t, initialErr)
						require.Nil(t, initial.Change)
						_, err = sub.Next(ctx)
						_, again := sub.Next(ctx)
						assert.Same(t, err, again)
					}
					var actual *events.Error
					require.ErrorAs(t, err, &actual)
					assert.Equal(t, code, actual.Code)
					assert.Equal(t, "mongo", actual.Backend)
					assert.Equal(t, "source", actual.SourceID)
					assert.Equal(t, "g", actual.Generation)
					assert.EqualValues(t, 3, actual.DiscardedThrough)
					assert.EqualValues(t, 8, actual.CommittedThrough)
					require.NotNil(t, errors.Unwrap(actual))
					assert.NotEqual(t, codes.OK, status.Code(errors.Unwrap(actual)))
					assert.NotContains(t, err.Error(), "private backend failure")
					assert.EqualValues(t, 1, registrations.Load())
				})
			}
		})
	}
}

func TestClosingOneGRPCSubscriptionLeavesOthersUsable(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	live := make(chan *events.PullerEvent, 1)
	client, server := serveClient(t, localSource(func(context.Context, events.SubscribeOptions) (events.Subscription, error) {
		return &localSubscription{next: func(ctx context.Context) (*events.PullerEvent, error) {
			select {
			case evt := <-live:
				return evt, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}}, nil
	}))
	first, err := client.Subscribe(ctx, events.SubscribeOptions{ConsumerID: "same-label"})
	require.NoError(t, err)
	second, err := client.Subscribe(ctx, events.SubscribeOptions{ConsumerID: "same-label"})
	require.NoError(t, err)
	defer second.Close()
	_, err = second.Next(ctx)
	require.NoError(t, err)
	require.NoError(t, first.Close())
	require.Eventually(t, func() bool { return server.SubscriberCount() == 1 }, time.Second, time.Millisecond)
	live <- &events.PullerEvent{Progress: "still-connected"}
	evt, err := second.Next(ctx)
	require.NoError(t, err)
	assert.Equal(t, "still-connected", evt.Progress)
}

func TestClientConfigurationAndIdempotentClose(t *testing.T) {
	t.Parallel()
	client, err := New("localhost:1", nil)
	require.NoError(t, err)
	assert.Equal(t, DefaultClientConfig(), client.cfg)
	require.NoError(t, client.Close())
	require.NoError(t, client.Close())
	for _, cfg := range []ClientConfig{{MaxReceiveBytes: -1}, {InitialBackoff: -1}, {MaxBackoff: time.Nanosecond}, {BackoffMultiplier: 0.5}, {MaxRetries: -1}} {
		_, err := NewWithConfig("localhost:1", nil, cfg)
		require.Error(t, err)
	}
}

func TestConvertEventRejectsInvalidPayloadAndPreservesProgressOnly(t *testing.T) {
	t.Parallel()
	heartbeat, err := convertEvent(&pullerv1.PullerEvent{Progress: "checkpoint"})
	require.NoError(t, err)
	assert.Equal(t, &events.PullerEvent{Progress: "checkpoint"}, heartbeat)
	for _, wire := range []*pullerv1.PullerEvent{nil, {ChangeEvent: &pullerv1.ChangeEvent{FullDoc: []byte("invalid")}}, {ChangeEvent: &pullerv1.ChangeEvent{UpdateDesc: []byte("invalid")}}} {
		_, err := convertEvent(wire)
		require.Error(t, err)
	}
	assert.True(t, retryable(io.EOF))
}

func TestGRPCReceiveLimitAcceptsConfiguredLargeEvents(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	payload := strings.Repeat("x", 5<<20)
	client, _ := serveClient(t, localSource(func(context.Context, events.SubscribeOptions) (events.Subscription, error) {
		delivered := false
		return &localSubscription{next: func(ctx context.Context) (*events.PullerEvent, error) {
			if !delivered {
				delivered = true
				return &events.PullerEvent{Change: &events.StoreChangeEvent{EventID: "large", FullDocument: &storage.StoredDoc{Data: map[string]any{"payload": payload}}}, Progress: "p1"}, nil
			}
			<-ctx.Done()
			return nil, ctx.Err()
		}}, nil
	}))
	sub, err := client.Subscribe(ctx, events.SubscribeOptions{})
	require.NoError(t, err)
	defer sub.Close()
	initial, err := sub.Next(ctx)
	require.NoError(t, err)
	require.Nil(t, initial.Change)
	event, err := sub.Next(ctx)
	require.NoError(t, err)
	require.NotNil(t, event.Change)
	assert.True(t, event.Change.FullDocument.Data["payload"] == payload)
	assert.Equal(t, 64<<20, client.cfg.MaxReceiveBytes)
}
