package pubsub

import (
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// SetJetStreamNew allows setting the jetStreamNew variable for testing.
func SetJetStreamNew(f func(nc *nats.Conn) (jetstream.JetStream, error)) func() {
	original := jetStreamNew
	jetStreamNew = f
	return func() {
		jetStreamNew = original
	}
}
