package core

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/syntrixbase/syntrix/internal/puller/cursor"
	"github.com/syntrixbase/syntrix/internal/puller/events"
)

func TestSubscriptionCommittedMemoryDelivery(t *testing.T) {
	cfg := newTestConfig(t)
	cfg.Buffer.BatchSize = 2
	p := newTestPuller(t, cfg, "primary")
	sub, initial := initialSubscription(t, p, events.SubscribeOptions{})
	require.Zero(t, sequence(t, initial, "primary-source"))
	event := testEvent(1)
	token := []byte{12, 0, 0, 0, 16, 'i', 0, 1, 0, 0, 0, 0}
	require.NoError(t, p.backends["primary"].buffer.Enqueue(testContext(t), event, token))
	p.mu.Lock()
	assert.Empty(t, sub.(*subscription).queue)
	position, _ := p.heads.GetPosition("primary-source")
	assert.Zero(t, position.Sequence)
	p.mu.Unlock()
	enqueueEvents(t, p, "primary", testEvent(2))
	for n := uint64(1); n <= 2; n++ {
		envelope := nextEnvelope(t, sub)
		assert.Equal(t, n, sequence(t, envelope.Progress, "primary-source"))
		assert.Equal(t, fmt.Sprint(n), envelope.Change.MgoDocID)
	}
}

func TestSubscriptionReplayThenLiveSameTimestamp(t *testing.T) {
	p := newTestPuller(t, newTestConfig(t), "primary")
	first, after := initialSubscription(t, p, events.SubscribeOptions{})
	require.NoError(t, first.Close())
	enqueueEvents(t, p, "primary", testEvent(1), testEvent(2), testEvent(3))
	sub, initial := initialSubscription(t, p, events.SubscribeOptions{After: after})
	assert.Equal(t, after, initial)
	enqueueEvents(t, p, "primary", testEvent(4), testEvent(5))
	for n := uint64(1); n <= 5; n++ {
		envelope := nextEnvelope(t, sub)
		assert.Equal(t, n, sequence(t, envelope.Progress, "primary-source"))
		assert.Equal(t, fmt.Sprint(n), envelope.Change.MgoDocID)
	}
}

func TestSubscriptionOverflowReplaysMissingPrefix(t *testing.T) {
	for _, limit := range []string{"count", "bytes"} {
		t.Run(limit, func(t *testing.T) {
			cfg := newTestConfig(t)
			if limit == "count" {
				cfg.GRPC.ChannelSize = 1
			} else {
				cfg.Consumer.QueueBytes = 1
			}
			p := newTestPuller(t, cfg, "primary")
			sub, _ := initialSubscription(t, p, events.SubscribeOptions{})
			enqueueEvents(t, p, "primary", testEvent(1), testEvent(2), testEvent(3), testEvent(4))
			p.mu.Lock()
			overflow, queued := sub.(*subscription).overflow, len(sub.(*subscription).queue)
			p.mu.Unlock()
			require.True(t, overflow)
			require.Zero(t, queued)
			for n := uint64(1); n <= 4; n++ {
				envelope := nextEnvelope(t, sub)
				assert.Equal(t, n, sequence(t, envelope.Progress, "primary-source"))
			}
			enqueueEvents(t, p, "primary", testEvent(5))
			assert.Equal(t, uint64(5), sequence(t, nextEnvelope(t, sub).Progress, "primary-source"))
		})
	}
}

func TestSubscriptionUniqueRegistrationAndCancellation(t *testing.T) {
	p := newTestPuller(t, newTestConfig(t), "primary")
	a, _ := initialSubscription(t, p, events.SubscribeOptions{ConsumerID: "same"})
	b, _ := initialSubscription(t, p, events.SubscribeOptions{ConsumerID: "same"})
	require.NotEqual(t, a.(*subscription).id, b.(*subscription).id)
	require.NoError(t, a.Close())
	require.NoError(t, a.Close())
	_, err := a.Next(testContext(t))
	require.ErrorIs(t, err, context.Canceled)
	enqueueEvents(t, p, "primary", testEvent(1))
	require.Equal(t, "1", nextEnvelope(t, b).Change.MgoDocID)
	cancelCtx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { _, err := b.Next(cancelCtx); result <- err }()
	cancel()
	select {
	case err := <-result:
		require.ErrorIs(t, err, context.Canceled)
	case <-testContext(t).Done():
		t.Fatal("Next did not cancel")
	}
	p.mu.Lock()
	assert.Empty(t, p.subscribers)
	p.mu.Unlock()
	_, err = b.Next(testContext(t))
	require.ErrorIs(t, err, context.Canceled)
}

func TestSubscriptionCursorValidation(t *testing.T) {
	p := newTestPuller(t, newTestConfig(t), "primary", "secondary")
	_, start := initialSubscription(t, p, events.SubscribeOptions{})
	tests := []struct {
		name   string
		mutate func(*cursor.ProgressMarker)
		code   events.ErrorCode
	}{
		{"missing", func(m *cursor.ProgressMarker) { delete(m.Positions, "secondary-source") }, events.CodeUnknownSource},
		{"unknown", func(m *cursor.ProgressMarker) {
			m.SetPosition(cursor.Position{SourceID: "other", Generation: "generation"})
		}, events.CodeUnknownSource},
		{"generation", func(m *cursor.ProgressMarker) {
			v := m.Positions["primary-source"]
			v.Generation = "changed"
			m.SetPosition(v)
		}, events.CodeGenerationMismatch},
		{"ahead", func(m *cursor.ProgressMarker) { v := m.Positions["primary-source"]; v.Sequence = 1; m.SetPosition(v) }, events.CodePositionAhead},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := marker(t, start)
			tt.mutate(m)
			token, err := m.Encode()
			require.NoError(t, err)
			_, err = p.Subscribe(testContext(t), events.SubscribeOptions{After: token})
			var domain *events.Error
			require.ErrorAs(t, err, &domain)
			assert.Equal(t, tt.code, domain.Code)
		})
	}
	_, err := p.Subscribe(testContext(t), events.SubscribeOptions{After: "%%%"})
	var domain *events.Error
	require.ErrorAs(t, err, &domain)
	assert.Equal(t, events.CodeInvalidCursor, domain.Code)
}

func TestSubscriptionIndependentSourcePositions(t *testing.T) {
	p := newTestPuller(t, newTestConfig(t), "primary", "secondary")
	sub, _ := initialSubscription(t, p, events.SubscribeOptions{})
	enqueueEvents(t, p, "secondary", testEvent(10))
	enqueueEvents(t, p, "primary", testEvent(20))
	first := nextEnvelope(t, sub)
	assert.Equal(t, uint64(1), sequence(t, first.Progress, "secondary-source"))
	assert.Zero(t, sequence(t, first.Progress, "primary-source"))
	second := nextEnvelope(t, sub)
	assert.Equal(t, uint64(1), sequence(t, second.Progress, "secondary-source"))
	assert.Equal(t, uint64(1), sequence(t, second.Progress, "primary-source"))
}

func TestSubscriptionExpiredHistoryAndTerminalFailure(t *testing.T) {
	p := newTestPuller(t, newTestConfig(t), "primary")
	sub, after := initialSubscription(t, p, events.SubscribeOptions{})
	require.NoError(t, sub.Close())
	enqueueEvents(t, p, "primary", testEvent(1), testEvent(2))
	require.NoError(t, p.backends["primary"].buffer.Retain(testContext(t), time.Now().Add(time.Hour), 0))
	_, err := p.Subscribe(testContext(t), events.SubscribeOptions{After: after})
	var domain *events.Error
	require.ErrorAs(t, err, &domain)
	assert.Equal(t, events.CodeHistoryExpired, domain.Code)
	live, _ := initialSubscription(t, p, events.SubscribeOptions{})
	failure := &events.Error{Code: events.CodeStorageFailure, Cause: errors.New("disk failed")}
	p.failBackend("primary", failure)
	for range 2 {
		_, err := live.Next(testContext(t))
		require.ErrorIs(t, err, failure)
	}
	require.ErrorIs(t, live.Close(), failure)
	_, err = p.Subscribe(testContext(t), events.SubscribeOptions{})
	require.ErrorIs(t, err, failure)
}

func TestSubscriptionRegistrationCommitRace(t *testing.T) {
	p := newTestPuller(t, newTestConfig(t), "primary")
	base, after := initialSubscription(t, p, events.SubscribeOptions{})
	require.NoError(t, base.Close())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for n := 1; n <= 20; n++ {
			enqueueEvents(t, p, "primary", testEvent(n))
		}
	}()
	sub, _ := initialSubscription(t, p, events.SubscribeOptions{After: after})
	for n := uint64(1); n <= 20; n++ {
		assert.Equal(t, n, sequence(t, nextEnvelope(t, sub).Progress, "primary-source"))
	}
	wg.Wait()
}

func TestSubscriptionLimitsAndParentCancellation(t *testing.T) {
	cfg := newTestConfig(t)
	cfg.GRPC.MaxConnections = 1
	p := newTestPuller(t, cfg, "primary")
	initializeTestBoundaries(t, p)
	ctx, cancel := context.WithCancel(context.Background())
	sub, err := p.Subscribe(ctx, events.SubscribeOptions{})
	require.NoError(t, err)
	_, err = p.Subscribe(testContext(t), events.SubscribeOptions{})
	var domain *events.Error
	require.ErrorAs(t, err, &domain)
	assert.Equal(t, events.CodeOverloaded, domain.Code)
	cancel()
	_, err = sub.Next(testContext(t))
	require.ErrorIs(t, err, context.Canceled)
	_, err = p.Subscribe(ctx, events.SubscribeOptions{})
	require.ErrorIs(t, err, context.Canceled)
}

func TestSubscriptionRequiresDurableInitialSourceBoundary(t *testing.T) {
	p := newTestPuller(t, newTestConfig(t), "primary")
	_, err := p.Subscribe(testContext(t), events.SubscribeOptions{})
	var domain *events.Error
	require.ErrorAs(t, err, &domain)
	assert.Equal(t, events.CodeSourceUnavailable, domain.Code)
	require.NoError(t, p.Err())
	initializeTestBoundaries(t, p)
	sub, err := p.Subscribe(testContext(t), events.SubscribeOptions{})
	require.NoError(t, err)
	require.NoError(t, sub.Close())
}
