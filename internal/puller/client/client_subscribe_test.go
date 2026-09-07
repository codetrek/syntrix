package client

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	pullerv1 "github.com/syntrixbase/syntrix/api/gen/puller/v1"
	"github.com/syntrixbase/syntrix/internal/puller/cursor"
	"github.com/syntrixbase/syntrix/internal/puller/events"
	grpcapi "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type rpcClientFunc func(context.Context, *pullerv1.SubscribeRequest) (pullerv1.PullerService_SubscribeClient, error)

func (f rpcClientFunc) Subscribe(ctx context.Context, req *pullerv1.SubscribeRequest, _ ...grpcapi.CallOption) (pullerv1.PullerService_SubscribeClient, error) {
	return f(ctx, req)
}

type rpcStream struct {
	initial string
	grpcapi.ClientStream
	recv   func() (*pullerv1.PullerEvent, error)
	header func() (metadata.MD, error)
}

func (s *rpcStream) Recv() (*pullerv1.PullerEvent, error) { return s.recv() }
func (s *rpcStream) Header() (metadata.MD, error) {
	if s.header != nil {
		return s.header()
	}
	initial := s.initial
	if initial == "" {
		initial = "start"
	}
	return metadata.Pairs("syntrix-puller-subscription", "ready", "syntrix-puller-initial-progress", initial), nil
}

func testClient(t *testing.T, client pullerv1.PullerServiceClient) *Client {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	c := &Client{client: client, cfg: ClientConfig{InitialBackoff: time.Millisecond, MaxBackoff: 2 * time.Millisecond, BackoffMultiplier: 2, MaxRetries: 2}, ctx: ctx, cancel: cancel}
	t.Cleanup(func() { require.NoError(t, c.Close()) })
	return c
}

func TestReconnectUsesOnlyDeliveredProgressIncludingProgressOnly(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	requests := []*pullerv1.SubscribeRequest{}
	rpc := rpcClientFunc(func(_ context.Context, req *pullerv1.SubscribeRequest) (pullerv1.PullerService_SubscribeClient, error) {
		requests = append(requests, req)
		switch len(requests) {
		case 1:
			index := 0
			return &rpcStream{initial: req.After, recv: func() (*pullerv1.PullerEvent, error) {
				index++
				if index == 1 {
					return &pullerv1.PullerEvent{ChangeEvent: &pullerv1.ChangeEvent{EventId: "e1"}, Progress: "p1"}, nil
				}
				if index == 2 {
					return &pullerv1.PullerEvent{Progress: "window-end"}, nil
				}
				return nil, status.Error(codes.Unavailable, "transport broke")
			}}, nil
		default:
			return &rpcStream{initial: req.After, recv: func() (*pullerv1.PullerEvent, error) {
				return &pullerv1.PullerEvent{ChangeEvent: &pullerv1.ChangeEvent{EventId: "e2"}, Progress: "p3"}, nil
			}}, nil
		}
	})
	client := testClient(t, rpc)
	sub, err := client.Subscribe(ctx, events.SubscribeOptions{ConsumerID: "consumer", After: "start", CoalesceOnCatchUp: true})
	require.NoError(t, err)
	defer sub.Close()
	first, err := sub.Next(ctx)
	require.NoError(t, err)
	assert.Equal(t, "e1", first.Change.EventID)
	progress, err := sub.Next(ctx)
	require.NoError(t, err)
	assert.Nil(t, progress.Change)
	assert.Equal(t, "window-end", progress.Progress)
	last, err := sub.Next(ctx)
	require.NoError(t, err)
	assert.Equal(t, "e2", last.Change.EventID)
	require.Len(t, requests, 2)
	assert.Equal(t, "start", requests[0].After)
	assert.Equal(t, "window-end", requests[1].After)
	assert.True(t, requests[1].CoalesceOnCatchUp)
	assert.Equal(t, "consumer", requests[1].ConsumerId)
}

func TestMalformedDeliveryIsTerminalWithoutAdvancingProgress(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	calls := 0
	c := testClient(t, rpcClientFunc(func(context.Context, *pullerv1.SubscribeRequest) (pullerv1.PullerService_SubscribeClient, error) {
		calls++
		return &rpcStream{initial: "safe", recv: func() (*pullerv1.PullerEvent, error) {
			return &pullerv1.PullerEvent{Progress: "unsafe", ChangeEvent: &pullerv1.ChangeEvent{FullDoc: []byte("invalid-json")}}, nil
		}}, nil
	}))
	sub, err := c.Subscribe(ctx, events.SubscribeOptions{After: "safe"})
	require.NoError(t, err)
	event, err := sub.Next(ctx)
	require.Nil(t, event)
	var domainErr *events.Error
	require.ErrorAs(t, err, &domainErr)
	assert.Equal(t, events.CodeUnsupportedFormat, domainErr.Code)
	assert.Equal(t, "safe", sub.(*subscription).progress)
	_, again := sub.Next(ctx)
	assert.Same(t, err, again)
	assert.Equal(t, 1, calls)
}

func TestReconnectBudgetIncludesStreamsThatFailBeforeDelivery(t *testing.T) {
	t.Parallel()
	for _, failAtHeader := range []bool{false, true} {
		t.Run(map[bool]string{false: "receive", true: "header"}[failAtHeader], func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			calls := 0
			failure := status.Error(codes.Unavailable, "offline")
			c := testClient(t, rpcClientFunc(func(context.Context, *pullerv1.SubscribeRequest) (pullerv1.PullerService_SubscribeClient, error) {
				calls++
				stream := &rpcStream{recv: func() (*pullerv1.PullerEvent, error) { return nil, failure }}
				if failAtHeader {
					stream.header = func() (metadata.MD, error) { return nil, nil }
				}
				return stream, nil
			}))
			sub, err := c.Subscribe(ctx, events.SubscribeOptions{})
			require.NoError(t, err)
			_, err = sub.Next(ctx)
			require.ErrorContains(t, err, "reconnect attempts exhausted")
			assert.ErrorIs(t, err, failure)
			assert.Equal(t, 3, calls)
		})
	}
}

func TestNextCancellationInterruptsRecvAndIsSticky(t *testing.T) {
	t.Parallel()
	lifetime, stop := context.WithTimeout(context.Background(), 3*time.Second)
	defer stop()
	entered := make(chan struct{})
	c := testClient(t, rpcClientFunc(func(ctx context.Context, _ *pullerv1.SubscribeRequest) (pullerv1.PullerService_SubscribeClient, error) {
		return &rpcStream{recv: func() (*pullerv1.PullerEvent, error) {
			close(entered)
			<-ctx.Done()
			return nil, status.FromContextError(ctx.Err()).Err()
		}}, nil
	}))
	sub, err := c.Subscribe(lifetime, events.SubscribeOptions{})
	require.NoError(t, err)
	call, cancel := context.WithCancel(lifetime)
	result := make(chan error, 1)
	go func() { _, err := sub.Next(call); result <- err }()
	select {
	case <-entered:
	case <-lifetime.Done():
		t.Fatal(lifetime.Err())
	}
	cancel()
	select {
	case err := <-result:
		require.ErrorIs(t, err, context.Canceled)
	case <-lifetime.Done():
		t.Fatal(lifetime.Err())
	}
	_, err = sub.Next(lifetime)
	require.ErrorIs(t, err, context.Canceled)
	require.NoError(t, sub.Close())
	require.NoError(t, sub.Close())
}

func TestClientCloseCancelsAllSubscriptionsAndRejectsNewOnes(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var entered atomic.Int32
	ready := make(chan struct{})
	c := testClient(t, rpcClientFunc(func(streamCtx context.Context, _ *pullerv1.SubscribeRequest) (pullerv1.PullerService_SubscribeClient, error) {
		return &rpcStream{recv: func() (*pullerv1.PullerEvent, error) {
			if entered.Add(1) == 2 {
				close(ready)
			}
			<-streamCtx.Done()
			return nil, context.Canceled
		}}, nil
	}))
	done := make(chan error, 2)
	for range 2 {
		sub, err := c.Subscribe(ctx, events.SubscribeOptions{})
		require.NoError(t, err)
		go func() { _, err := sub.Next(ctx); done <- err }()
	}
	select {
	case <-ready:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	require.NoError(t, c.Close())
	for range 2 {
		select {
		case err := <-done:
			require.ErrorIs(t, err, context.Canceled)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	_, err := c.Subscribe(ctx, events.SubscribeOptions{})
	require.ErrorIs(t, err, context.Canceled)
}

func TestRetryClassificationDoesNotHideApplicationFailures(t *testing.T) {
	t.Parallel()
	assert.True(t, retryable(io.EOF))
	assert.True(t, retryable(status.Error(codes.Unavailable, "connection lost")))
	for _, err := range []error{context.Canceled, context.DeadlineExceeded, errors.New("unexpected failure"), status.Error(codes.Internal, "bug"), status.Error(codes.PermissionDenied, "denied"), status.Error(codes.ResourceExhausted, "overload")} {
		assert.False(t, retryable(err), "%v", err)
	}
}

func TestSubscribeCancellationInterruptsRegistration(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	entered := make(chan struct{})
	c := testClient(t, rpcClientFunc(func(streamCtx context.Context, _ *pullerv1.SubscribeRequest) (pullerv1.PullerService_SubscribeClient, error) {
		return &rpcStream{header: func() (metadata.MD, error) {
			close(entered)
			<-streamCtx.Done()
			return nil, status.FromContextError(streamCtx.Err()).Err()
		}}, nil
	}))
	call, stop := context.WithCancel(ctx)
	result := make(chan error, 1)
	go func() { _, err := c.Subscribe(call, events.SubscribeOptions{}); result <- err }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	stop()
	select {
	case err := <-result:
		require.ErrorIs(t, err, context.Canceled)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func TestCancellationInterruptsReconnectBackoff(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	started := make(chan struct{})
	c := testClient(t, rpcClientFunc(func(context.Context, *pullerv1.SubscribeRequest) (pullerv1.PullerService_SubscribeClient, error) {
		return nil, status.Error(codes.Unavailable, "offline")
	}))
	c.cfg.InitialBackoff, c.cfg.MaxBackoff = time.Hour, time.Hour
	c.cfg.OnStateChange = func(state ConnectionState, err error) {
		if state == StateReconnecting {
			close(started)
		}
	}
	sub, err := c.Subscribe(ctx, events.SubscribeOptions{})
	require.NoError(t, err)
	result := make(chan error, 1)
	go func() { _, err := sub.Next(ctx); result <- err }()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	require.NoError(t, sub.Close())
	select {
	case err := <-result:
		require.ErrorIs(t, err, context.Canceled)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func TestReconnectBeforeFirstDeliveryUsesRegistrationAnchor(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var requests []string
	c := testClient(t, rpcClientFunc(func(_ context.Context, req *pullerv1.SubscribeRequest) (pullerv1.PullerService_SubscribeClient, error) {
		requests = append(requests, req.After)
		if len(requests) == 1 {
			return &rpcStream{initial: "position-10", recv: func() (*pullerv1.PullerEvent, error) {
				return nil, status.Error(codes.Unavailable, "disconnected after registration")
			}}, nil
		}
		return &rpcStream{initial: req.After, recv: func() (*pullerv1.PullerEvent, error) {
			return &pullerv1.PullerEvent{ChangeEvent: &pullerv1.ChangeEvent{EventId: "event-11"}, Progress: "position-11"}, nil
		}}, nil
	}))
	sub, err := c.Subscribe(ctx, events.SubscribeOptions{})
	require.NoError(t, err)
	defer sub.Close()
	event, err := sub.Next(ctx)
	require.NoError(t, err)
	assert.Equal(t, "event-11", event.Change.EventID)
	assert.Equal(t, []string{"", "position-10"}, requests)
}

func TestRegistrationEnvelopesDoNotResetReconnectBudget(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	calls := 0
	c := testClient(t, rpcClientFunc(func(context.Context, *pullerv1.SubscribeRequest) (pullerv1.PullerService_SubscribeClient, error) {
		calls++
		initial := true
		return &rpcStream{initial: "unchanged", recv: func() (*pullerv1.PullerEvent, error) {
			if initial {
				initial = false
				return &pullerv1.PullerEvent{Progress: "unchanged"}, nil
			}
			return nil, status.Error(codes.Unavailable, "dropped after registration")
		}}, nil
	}))
	sub, err := c.Subscribe(ctx, events.SubscribeOptions{})
	require.NoError(t, err)
	defer sub.Close()
	for range 3 {
		event, err := sub.Next(ctx)
		require.NoError(t, err)
		assert.Nil(t, event.Change)
		assert.Equal(t, "unchanged", event.Progress)
	}
	_, err = sub.Next(ctx)
	require.ErrorContains(t, err, "reconnect attempts exhausted")
	assert.Equal(t, 3, calls)
}

func TestRegistrationAcceptsEquivalentCursorEncoding(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	requested := base64.RawURLEncoding.EncodeToString([]byte(`{ "positions": { "source": {"sequence":10,"generation":"g","source":"source"} }, "v":1 }`))
	canonical := base64.RawURLEncoding.EncodeToString([]byte(`{"v":1,"positions":{"source":{"source":"source","generation":"g","sequence":10}}}`))
	c := testClient(t, rpcClientFunc(func(_ context.Context, req *pullerv1.SubscribeRequest) (pullerv1.PullerService_SubscribeClient, error) {
		assert.Equal(t, requested, req.After)
		return &rpcStream{initial: canonical, recv: func() (*pullerv1.PullerEvent, error) { return &pullerv1.PullerEvent{Progress: canonical}, nil }}, nil
	}))
	sub, err := c.Subscribe(ctx, events.SubscribeOptions{After: requested})
	require.NoError(t, err)
	defer sub.Close()
	event, err := sub.Next(ctx)
	require.NoError(t, err)
	assert.Equal(t, canonical, event.Progress)
}

func TestRegistrationRejectsInvalidOrChangedStartingPosition(t *testing.T) {
	t.Parallel()
	encode := func(positions ...cursor.Position) string {
		marker := cursor.NewProgressMarker()
		for _, position := range positions {
			marker.SetPosition(position)
		}
		encoded, err := marker.Encode()
		require.NoError(t, err)
		return encoded
	}
	source := cursor.Position{SourceID: "source", Generation: "generation", Sequence: 10}
	extra := cursor.Position{SourceID: "other", Generation: "other-generation", Sequence: 20}
	requested := encode(source)
	changedSource := source
	changedSource.SourceID = "replacement"
	changedGeneration := source
	changedGeneration.Generation = "new-generation"
	regressed := source
	regressed.Sequence--
	advanced := source
	advanced.Sequence++
	cases := []struct {
		name      string
		requested string
		initial   string
		code      events.ErrorCode
	}{
		{"empty initial position", requested, "", events.CodeUnsupportedFormat},
		{"changed source", requested, encode(changedSource), events.CodeInvalidCursor},
		{"changed generation", requested, encode(changedGeneration), events.CodeInvalidCursor},
		{"regressed sequence", requested, encode(regressed), events.CodeInvalidCursor},
		{"advanced sequence", requested, encode(advanced), events.CodeInvalidCursor},
		{"missing source", encode(source, extra), requested, events.CodeInvalidCursor},
		{"added source", requested, encode(source, extra), events.CodeInvalidCursor},
		{"malformed requested cursor", "malformed-cursor", requested, events.CodeInvalidCursor},
		{"malformed returned cursor", requested, "malformed-cursor", events.CodeInvalidCursor},
		{"missing generation", requested, base64.RawURLEncoding.EncodeToString([]byte(`{"v":1,"positions":{"source":{"source":"source","sequence":10}}}`)), events.CodeInvalidCursor},
		{"unsupported returned format", requested, base64.RawURLEncoding.EncodeToString([]byte(`{"v":2,"positions":{"source":{"source":"source","generation":"generation","sequence":10}}}`)), events.CodeUnsupportedFormat},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			calls, receives := 0, 0
			c := testClient(t, rpcClientFunc(func(_ context.Context, req *pullerv1.SubscribeRequest) (pullerv1.PullerService_SubscribeClient, error) {
				calls++
				assert.Equal(t, test.requested, req.After)
				return &rpcStream{
					header: func() (metadata.MD, error) {
						return metadata.Pairs("syntrix-puller-subscription", "ready", "syntrix-puller-initial-progress", test.initial), nil
					},
					recv: func() (*pullerv1.PullerEvent, error) { receives++; return nil, io.EOF },
				}, nil
			}))
			sub, err := c.Subscribe(ctx, events.SubscribeOptions{After: test.requested})
			require.Nil(t, sub)
			var failure *events.Error
			require.ErrorAs(t, err, &failure)
			assert.Equal(t, test.code, failure.Code)
			assert.Equal(t, 1, calls)
			assert.Zero(t, receives)
		})
	}
}

func TestRejectedReconnectAnchorPreservesDeliveredPositionAndStops(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	initial := base64.RawURLEncoding.EncodeToString([]byte(`{"v":1,"positions":{"source":{"source":"source","generation":"g","sequence":10}}}`))
	advanced := base64.RawURLEncoding.EncodeToString([]byte(`{"v":1,"positions":{"source":{"source":"source","generation":"g","sequence":11}}}`))
	var requests []string
	c := testClient(t, rpcClientFunc(func(_ context.Context, req *pullerv1.SubscribeRequest) (pullerv1.PullerService_SubscribeClient, error) {
		requests = append(requests, req.After)
		if len(requests) == 1 {
			return &rpcStream{initial: initial, recv: func() (*pullerv1.PullerEvent, error) {
				return nil, status.Error(codes.Unavailable, "connection lost before delivery")
			}}, nil
		}
		return &rpcStream{initial: advanced, recv: func() (*pullerv1.PullerEvent, error) {
			t.Error("invalid reconnect must not receive events")
			return nil, io.EOF
		}}, nil
	}))
	sub, err := c.Subscribe(ctx, events.SubscribeOptions{})
	require.NoError(t, err)
	defer sub.Close()
	event, err := sub.Next(ctx)
	require.Nil(t, event)
	var failure *events.Error
	require.ErrorAs(t, err, &failure)
	assert.Equal(t, events.CodeInvalidCursor, failure.Code)
	assert.Equal(t, initial, sub.(*subscription).progress)
	assert.Equal(t, 1, sub.(*subscription).failures)
	_, again := sub.Next(ctx)
	assert.Same(t, err, again)
	assert.Equal(t, []string{"", initial}, requests)
}
