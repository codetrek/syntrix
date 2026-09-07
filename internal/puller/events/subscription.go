package events

import "context"

// SubscribeOptions selects a source position and catch-up policy. ConsumerID is
// diagnostic identity; concurrent subscriptions may use the same value.
type SubscribeOptions struct {
	ConsumerID        string
	After             string
	CoalesceOnCatchUp bool
}

// Subscription delivers immutable committed records and progress-only envelopes.
// A returned progress marker becomes a processed checkpoint only after the caller
// successfully applies all preceding deliveries. Next calls must be serialized.
// Canceling Next terminates this subscription; Close is idempotent.
type Subscription interface {
	Next(context.Context) (*PullerEvent, error)
	Close() error
}
