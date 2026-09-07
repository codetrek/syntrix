package core

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/syntrixbase/syntrix/internal/puller/buffer"
	"github.com/syntrixbase/syntrix/internal/puller/cursor"
	"github.com/syntrixbase/syntrix/internal/puller/events"
)

func TestCoalescedWindowCheckpointWaitsForLastOutput(t *testing.T) {
	cfg := newTestConfig(t)
	cfg.Consumer.PageSize = 10
	cfg.Consumer.CatchUpThreshold = 1
	p := newTestPuller(t, cfg, "primary")
	initial, after := initialSubscription(t, p, events.SubscribeOptions{})
	require.NoError(t, initial.Close())
	a, b, updated := testEvent(1), testEvent(2), testEvent(1)
	updated.OpType = events.StoreOperationUpdate
	enqueueEvents(t, p, "primary", a, b, updated)
	sub, _ := initialSubscription(t, p, events.SubscribeOptions{After: after, CoalesceOnCatchUp: true})
	first := nextEnvelope(t, sub)
	assert.Equal(t, "2", first.Change.MgoDocID)
	assert.Zero(t, sequence(t, first.Progress, "primary-source"))
	last := nextEnvelope(t, sub)
	assert.Equal(t, "1", last.Change.MgoDocID)
	assert.Equal(t, events.StoreOperationInsert, last.Change.OpType)
	assert.Equal(t, "primary", last.Change.Backend)
	assert.Equal(t, uint64(3), sequence(t, last.Progress, "primary-source"))
}

func TestCoalescingChangingRetryWindowPreservesDelete(t *testing.T) {
	cfg := newTestConfig(t)
	cfg.Consumer.PageSize = 10
	cfg.Consumer.CatchUpThreshold = 1
	p := newTestPuller(t, cfg, "primary")
	initial, after := initialSubscription(t, p, events.SubscribeOptions{})
	require.NoError(t, initial.Close())
	enqueueEvents(t, p, "primary", testEvent(1), testEvent(2))
	sub, _ := initialSubscription(t, p, events.SubscribeOptions{After: after, CoalesceOnCatchUp: true})
	first := nextEnvelope(t, sub)
	assert.Equal(t, "1", first.Change.MgoDocID)
	assert.Zero(t, sequence(t, first.Progress, "primary-source"))
	require.NoError(t, sub.Close())
	deleted := testEvent(1)
	deleted.OpType = events.StoreOperationDelete
	enqueueEvents(t, p, "primary", deleted)
	resumed, _ := initialSubscription(t, p, events.SubscribeOptions{After: first.Progress, CoalesceOnCatchUp: true})
	state := map[string]bool{"1": true}
	for range 2 {
		event := nextEnvelope(t, resumed)
		if event.Change.OpType == events.StoreOperationDelete {
			delete(state, event.Change.MgoDocID)
		} else {
			state[event.Change.MgoDocID] = true
		}
	}
	assert.Equal(t, map[string]bool{"2": true}, state)
}

func TestCoalescingRequiresPolicyRequestAndThreshold(t *testing.T) {
	cases := []struct {
		name            string
		policy, request bool
		threshold       int
		want            int
	}{
		{"enabled", true, true, 1, 1}, {"policy", false, true, 1, 2}, {"request", true, false, 1, 2}, {"threshold", true, true, 100, 2},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			cfg := newTestConfig(t)
			cfg.Consumer.PageSize = 10
			cfg.Consumer.CoalesceOnCatchUp = tt.policy
			cfg.Consumer.CatchUpThreshold = tt.threshold
			p := newTestPuller(t, cfg, "primary")
			start, after := initialSubscription(t, p, events.SubscribeOptions{})
			require.NoError(t, start.Close())
			updated := testEvent(1)
			updated.OpType = events.StoreOperationUpdate
			enqueueEvents(t, p, "primary", testEvent(1), updated)
			sub, _ := initialSubscription(t, p, events.SubscribeOptions{After: after, CoalesceOnCatchUp: tt.request})
			var last *events.PullerEvent
			for range tt.want {
				last = nextEnvelope(t, sub)
			}
			assert.Equal(t, uint64(2), sequence(t, last.Progress, "primary-source"))
		})
	}
}

func TestReplayRetentionRecheckedBetweenPages(t *testing.T) {
	cfg := newTestConfig(t)
	cfg.Consumer.PageSize = 1
	p := newTestPuller(t, cfg, "primary")
	start, after := initialSubscription(t, p, events.SubscribeOptions{})
	require.NoError(t, start.Close())
	enqueueEvents(t, p, "primary", testEvent(1), testEvent(2), testEvent(3))
	sub, _ := initialSubscription(t, p, events.SubscribeOptions{After: after})
	assert.Equal(t, uint64(1), sequence(t, nextEnvelope(t, sub).Progress, "primary-source"))
	require.NoError(t, p.backends["primary"].buffer.Retain(testContext(t), time.Now().Add(time.Hour), 0))
	_, err := sub.Next(testContext(t))
	var domain *events.Error
	require.ErrorAs(t, err, &domain)
	assert.Equal(t, events.CodeHistoryExpired, domain.Code)
	require.NoError(t, p.Err())
}

func TestLiveDeliveryDoesNotReadPebble(t *testing.T) {
	p := newTestPuller(t, newTestConfig(t), "primary")
	sub, _ := initialSubscription(t, p, events.SubscribeOptions{})
	enqueueEvents(t, p, "primary", testEvent(1))
	require.NoError(t, p.backends["primary"].buffer.Close(testContext(t)))
	envelope := nextEnvelope(t, sub)
	assert.Equal(t, "1", envelope.Change.MgoDocID)
	assert.Equal(t, uint64(1), sequence(t, envelope.Progress, "primary-source"))
}

func TestReplayStorageErrorFailsAllSubscriptions(t *testing.T) {
	p := newTestPuller(t, newTestConfig(t), "primary")
	start, after := initialSubscription(t, p, events.SubscribeOptions{})
	require.NoError(t, start.Close())
	enqueueEvents(t, p, "primary", testEvent(1))
	sub, _ := initialSubscription(t, p, events.SubscribeOptions{After: after})
	other, _ := initialSubscription(t, p, events.SubscribeOptions{})
	require.NoError(t, p.backends["primary"].buffer.Close(testContext(t)))
	_, err := sub.Next(testContext(t))
	var domain *events.Error
	require.ErrorAs(t, err, &domain)
	assert.Equal(t, events.CodeStorageFailure, domain.Code)
	_, err = other.Next(testContext(t))
	require.ErrorAs(t, err, &domain)
	require.Error(t, p.Err())
}

func TestSubscriptionDeadlineIsSticky(t *testing.T) {
	p := newTestPuller(t, newTestConfig(t), "primary")
	sub, _ := initialSubscription(t, p, events.SubscribeOptions{})
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	_, err := sub.Next(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	_, err = sub.Next(testContext(t))
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestReplayCancellationDuringPageReadPreservesCheckpoint(t *testing.T) {
	for _, owner := range []string{"caller", "subscription"} {
		t.Run(owner, func(t *testing.T) {
			p := newTestPuller(t, newTestConfig(t), "primary")
			original, after := initialSubscription(t, p, events.SubscribeOptions{})
			require.NoError(t, original.Close())
			enqueueEvents(t, p, "primary", testEvent(1), testEvent(2))
			parentCtx, cancelParent := context.WithCancel(testContext(t))
			defer cancelParent()
			sub, err := p.Subscribe(parentCtx, events.SubscribeOptions{After: after})
			require.NoError(t, err)
			initial := nextEnvelope(t, sub)
			require.Equal(t, after, initial.Progress)
			backend := p.backends["primary"]
			entered := make(chan struct{})
			backend.readPage = func(ctx context.Context, after, through cursor.Position, maxEvents int, maxBytes int64) (buffer.Page, error) {
				close(entered)
				<-ctx.Done()
				return backend.buffer.ReadPage(ctx, after, through, maxEvents, maxBytes)
			}
			callerCtx, cancelCaller := context.WithCancel(testContext(t))
			defer cancelCaller()
			result := make(chan error, 1)
			go func() { _, err := sub.Next(callerCtx); result <- err }()
			select {
			case <-entered:
			case <-testContext(t).Done():
				t.Fatal("replay did not start reading")
			}
			if owner == "caller" {
				cancelCaller()
			} else {
				cancelParent()
			}
			select {
			case err := <-result:
				require.ErrorIs(t, err, context.Canceled)
			case <-testContext(t).Done():
				t.Fatal("replay did not cancel")
			}
			require.NoError(t, p.Err(), "a canceled read does not damage the source")
			_, err = sub.Next(testContext(t))
			require.ErrorIs(t, err, context.Canceled)
			resumed, _ := initialSubscription(t, p, events.SubscribeOptions{After: after})
			backend.readPage = backend.buffer.ReadPage
			event := nextEnvelope(t, resumed)
			require.Equal(t, "1", event.Change.MgoDocID)
			require.Equal(t, uint64(1), sequence(t, event.Progress, "primary-source"))
		})
	}
}
