package pubsub

import (
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// jetStreamNew is a variable to allow mocking jetstream.New in tests.
var jetStreamNew = func(nc *nats.Conn) (jetstream.JetStream, error) {
	return jetstream.New(nc)
}
