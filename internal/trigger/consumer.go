package trigger

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// Consumer consumes delivery tasks from NATS and dispatches them to the worker.
type Consumer struct {
	js     jetstream.JetStream
	worker Worker
	stream string
}

// NewConsumer creates a new Consumer.
func NewConsumer(nc *nats.Conn, worker Worker) (*Consumer, error) {
	js, err := jetstream.New(nc)
	if err != nil {
		return nil, err
	}

	return &Consumer{
		js:     js,
		worker: worker,
		stream: "TRIGGERS",
	}, nil
}

// Start begins consuming messages. It blocks until the context is cancelled.
func (c *Consumer) Start(ctx context.Context) error {
	// Ensure Stream exists
	// In production, streams should be managed by IaC or migration tools.
	// Here we ensure it exists for development convenience.
	_, err := c.js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:      c.stream,
		Subjects:  []string{"triggers.>"},
		Storage:   jetstream.FileStorage,
		Retention: jetstream.WorkQueuePolicy, // WorkQueue policy ensures each message is processed by only one consumer
	})
	if err != nil {
		return fmt.Errorf("failed to ensure stream: %w", err)
	}

	// Create Consumer
	consumer, err := c.js.CreateOrUpdateConsumer(ctx, c.stream, jetstream.ConsumerConfig{
		Durable:       "TriggerDeliveryWorker",
		AckPolicy:     jetstream.AckExplicitPolicy,
		FilterSubject: "triggers.>",
	})
	if err != nil {
		return fmt.Errorf("failed to create consumer: %w", err)
	}

	// Consume messages
	iter, err := consumer.Messages(jetstream.PullMaxMessages(1))
	if err != nil {
		return fmt.Errorf("failed to create message iterator: %w", err)
	}
	defer iter.Stop()

	log.Println("Trigger Consumer started, waiting for messages...")

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
			// Fetch next message
			msg, err := iter.Next()
			if err != nil {
				// Timeout or other error, just retry loop
				continue
			}

			// Process message
			if err := c.processMsg(ctx, msg); err != nil {
				log.Printf("[Error] Failed to process message: %v", err)
				// Nak with delay? Or Terminate if fatal?
				// For now, Nak so it's redelivered.
				msg.Nak()
			} else {
				msg.Ack()
			}
		}
	}
}

func (c *Consumer) processMsg(ctx context.Context, msg jetstream.Msg) error {
	var task DeliveryTask
	if err := json.Unmarshal(msg.Data(), &task); err != nil {
		// If payload is invalid, we should probably Terminate it to avoid infinite loop.
		// But for safety, let's log and return error.
		return fmt.Errorf("invalid payload: %w", err)
	}

	log.Printf("[Info] Processing trigger task: %s", task.TriggerID)

	// Execute task
	// We create a new context with timeout for the task execution
	taskCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	return c.worker.ProcessTask(taskCtx, &task)
}
