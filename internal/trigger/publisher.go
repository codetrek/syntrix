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

// natsPublisher implements EventPublisher using NATS JetStream.
type natsPublisher struct {
	js jetstream.JetStream
}

func NewEventPublisher(nc *nats.Conn) (EventPublisher, error) {
	js, err := jetstream.New(nc)
	if err != nil {
		return nil, err
	}
	return &natsPublisher{js: js}, nil
}

func (p *natsPublisher) Publish(ctx context.Context, task *DeliveryTask) error {
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
