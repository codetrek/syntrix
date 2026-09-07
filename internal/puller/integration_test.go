package puller

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/syntrixbase/syntrix/internal/puller/cursor"
	"github.com/syntrixbase/syntrix/internal/puller/events"
)

func TestPuller_Integration_ReplayToLiveAcrossTransports(t *testing.T) {
	env := newPullerIntegrationEnv(t, nil)
	observer := subscribeIntegration(t, env.ctx, env.local, "")
	env.insert(1, 1)
	anchor := nextIntegration(t, env.ctx, observer)
	env.insert(2, 10)
	for i := 2; i <= 11; i++ {
		require.Equal(t, fmt.Sprintf("doc-%d", i), nextIntegration(t, env.ctx, observer).Change.MgoDocID)
	}

	local := subscribeIntegration(t, env.ctx, env.local, anchor.Progress)
	remote := subscribeIntegration(t, env.ctx, env.remote, anchor.Progress)
	// Both registrations capture history through 11. New commits accumulate
	// while the local subscriber is still replaying its three-record pages.
	env.insert(12, 10)
	for i := 2; i <= 21; i++ {
		localEvent := nextIntegration(t, env.ctx, local)
		remoteEvent := nextIntegration(t, env.ctx, remote)
		assert.Equal(t, fmt.Sprintf("doc-%d", i), localEvent.Change.MgoDocID)
		assert.Equal(t, localEvent.Change.EventID, remoteEvent.Change.EventID)
		assert.Equal(t, localEvent.Progress, remoteEvent.Progress)
		assert.Equal(t, uint64(i), eventPosition(t, localEvent).Sequence)
	}
	env.insert(22, 1)
	assert.Equal(t, "doc-22", nextIntegration(t, env.ctx, local).Change.MgoDocID)
	assert.Equal(t, "doc-22", nextIntegration(t, env.ctx, remote).Change.MgoDocID)
}

func TestPuller_Integration_CursorFailuresMatchAcrossTransports(t *testing.T) {
	env := newPullerIntegrationEnv(t, nil)
	observer := subscribeIntegration(t, env.ctx, env.local, "")
	env.insert(1, 1)
	anchor := nextIntegration(t, env.ctx, observer)
	position := eventPosition(t, anchor)
	encode := func(position cursor.Position) string {
		marker := cursor.NewProgressMarker()
		marker.SetPosition(position)
		value, err := marker.Encode()
		require.NoError(t, err)
		return value
	}
	unknown := position
	unknown.SourceID = "another-source"
	stale := position
	stale.Generation = "another-generation"
	ahead := position
	ahead.Sequence++

	cases := []struct {
		name  string
		after string
		code  string
	}{
		{name: "Malformed", after: "!", code: "INVALID_CURSOR"},
		{name: "UnknownSource", after: encode(unknown), code: "UNKNOWN_SOURCE"},
		{name: "GenerationMismatch", after: encode(stale), code: "GENERATION_MISMATCH"},
		{name: "PositionAhead", after: encode(ahead), code: "POSITION_AHEAD"},
	}
	for _, transport := range []struct {
		name    string
		service Service
	}{{name: "local", service: env.local}, {name: "grpc", service: env.remote}} {
		t.Run(transport.name, func(t *testing.T) {
			for _, test := range cases {
				t.Run(test.name, func(t *testing.T) {
					ctx, cancel := context.WithTimeout(env.ctx, 3*time.Second)
					defer cancel()
					sub, err := transport.service.Subscribe(ctx, events.SubscribeOptions{ConsumerID: t.Name(), After: test.after})
					if err == nil {
						defer sub.Close()
						_, err = sub.Next(ctx)
					}
					var domainError *events.Error
					require.ErrorAs(t, err, &domainError)
					assert.Equal(t, test.code, string(domainError.Code))
					assert.NoError(t, ctx.Err(), "terminal cursor failures must surface before request timeout")
				})
			}
		})
	}
}

func TestPuller_Integration_ReconnectBeforeFirstDelivery(t *testing.T) {
	env := newPullerIntegrationEnv(t, nil)
	observer := subscribeIntegration(t, env.ctx, env.local, "")
	remote, err := env.remote.Subscribe(env.ctx, events.SubscribeOptions{ConsumerID: t.Name()})
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, remote.Close()) })

	// No Next call has delivered the starting cursor. Headers must retain that
	// anchor so a transport restart cannot replace it with a newer live head.
	env.stopTransport()
	env.insert(1, 5)
	for i := 1; i <= 5; i++ {
		require.Equal(t, fmt.Sprintf("doc-%d", i), nextIntegration(t, env.ctx, observer).Change.MgoDocID)
	}
	env.startTransport(env.address)
	for i := 1; i <= 5; i++ {
		event := nextIntegration(t, env.ctx, remote)
		assert.Equal(t, fmt.Sprintf("doc-%d", i), event.Change.MgoDocID)
		assert.Equal(t, uint64(i), eventPosition(t, event).Sequence)
	}
}
