package trigger

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// EventPublisher defines the interface for publishing delivery tasks.
type EventPublisher interface {
	Publish(ctx context.Context, task *DeliveryTask) error
}

// NatsPublisher implements EventPublisher using NATS JetStream.
type NatsPublisher struct {
	js jetstream.JetStream
}

func NewNatsPublisher(nc *nats.Conn) (*NatsPublisher, error) {
	js, err := jetstream.New(nc)
	if err != nil {
		return nil, err
	}
	return &NatsPublisher{js: js}, nil
}

func (p *NatsPublisher) Publish(ctx context.Context, task *DeliveryTask) error {
	// Subject format: triggers.<tenant>.<collection>.<docKey>
	// Note: docKey might contain dots, so we might need to sanitize or use a different separator if NATS wildcards are used for routing.
	// For now, assuming docKey is safe or we accept the structure.
	subject := fmt.Sprintf("triggers.%s.%s.%s", task.Tenant, task.Collection, task.DocKey)

	data, err := json.Marshal(task)
	if err != nil {
		return err
	}

	_, err = p.js.Publish(ctx, subject, data)
	return err
}
