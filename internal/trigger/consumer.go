package trigger

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"log"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// Consumer consumes delivery tasks from NATS and dispatches them to the worker.
type Consumer struct {
	js          jetstream.JetStream
	worker      Worker
	stream      string
	numWorkers  int
	workerChans []chan jetstream.Msg
	wg          sync.WaitGroup
}

// NewConsumer creates a new Consumer.
func NewConsumer(nc *nats.Conn, worker Worker, numWorkers int) (*Consumer, error) {
	js, err := jetstream.New(nc)
	if err != nil {
		return nil, err
	}

	if numWorkers <= 0 {
		numWorkers = 16
	}

	return &Consumer{
		js:         js,
		worker:     worker,
		stream:     "TRIGGERS",
		numWorkers: numWorkers,
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

	// Initialize Worker Pool
	c.workerChans = make([]chan jetstream.Msg, c.numWorkers)
	for i := 0; i < c.numWorkers; i++ {
		c.workerChans[i] = make(chan jetstream.Msg, 100) // Buffer size 100
		c.wg.Add(1)
		go c.workerLoop(ctx, i)
	}

	// Consume messages using Consume (Push-like callback)
	// This is non-blocking and handles the pull loop internally.
	cc, err := consumer.Consume(func(msg jetstream.Msg) {
		c.dispatch(msg)
	})
	if err != nil {
		return fmt.Errorf("failed to start consumer: %w", err)
	}
	defer cc.Stop()

	log.Printf("Trigger Consumer started with %d workers, waiting for messages...", c.numWorkers)

	// Block until context is done
	<-ctx.Done()

	// Shutdown
	log.Println("Stopping Trigger Consumer...")
	cc.Stop() // Stop consuming new messages

	// Close all worker channels
	for _, ch := range c.workerChans {
		close(ch)
	}
	c.wg.Wait() // Wait for workers to drain
	return nil
}

func (c *Consumer) dispatch(msg jetstream.Msg) {
	// Peek at payload to determine partition key
	var task DeliveryTask
	if err := json.Unmarshal(msg.Data(), &task); err != nil {
		log.Printf("[Error] Invalid payload in dispatch: %v", err)
		msg.Term() // Terminate invalid messages
		return
	}

	// Hash Collection Path to select worker
	// This ensures all events for the same collection go to the same worker (serial execution)
	h := fnv.New32a()
	h.Write([]byte(task.Collection))
	hash := h.Sum32()
	workerIdx := int(hash % uint32(c.numWorkers))

	// Send to worker
	c.workerChans[workerIdx] <- msg
}

func (c *Consumer) workerLoop(ctx context.Context, id int) {
	defer c.wg.Done()

	for msg := range c.workerChans[id] {
		// Process message
		if err := c.processMsg(ctx, msg); err != nil {
			log.Printf("[Error] [Worker %d] Failed to process message: %v", id, err)

			// Retry Logic
			md, metaErr := msg.Metadata()
			if metaErr != nil {
				log.Printf("[Error] Failed to get message metadata: %v", metaErr)
				msg.Nak()
				continue
			}

			var task DeliveryTask
			if jsonErr := json.Unmarshal(msg.Data(), &task); jsonErr != nil {
				log.Printf("[Error] Invalid payload for retry check: %v", jsonErr)
				msg.Term()
				continue
			}

			// Check Max Attempts
			// NumDelivered starts at 1
			maxAttempts := task.RetryPolicy.MaxAttempts
			if maxAttempts == 0 {
				maxAttempts = 3 // Default
			}

			if int(md.NumDelivered) >= maxAttempts {
				log.Printf("[Error] Max attempts (%d) reached for trigger %s. Terminating.", maxAttempts, task.TriggerID)
				msg.Term()
				continue
			}

			// Calculate Backoff
			attempt := int(md.NumDelivered)
			initialBackoff := time.Duration(task.RetryPolicy.InitialBackoff)
			if initialBackoff == 0 {
				initialBackoff = 1 * time.Second
			}

			// Exponential backoff: initial * 2^(attempt-1)
			backoff := initialBackoff * (1 << (attempt - 1))

			maxBackoff := time.Duration(task.RetryPolicy.MaxBackoff)
			if maxBackoff > 0 && backoff > maxBackoff {
				backoff = maxBackoff
			}

			log.Printf("[Info] Retrying trigger %s in %v (Attempt %d/%d)", task.TriggerID, backoff, attempt+1, maxAttempts)
			msg.NakWithDelay(backoff)
		} else {
			msg.Ack()
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
	timeout := time.Duration(task.Timeout)
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	taskCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	return c.worker.ProcessTask(taskCtx, &task)
}
