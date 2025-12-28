package pubsub

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"log"
	"sync"
	"time"

	"github.com/codetrek/syntrix/internal/trigger/internal/worker"
	"github.com/codetrek/syntrix/internal/trigger/types"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// natsConsumer consumes delivery tasks from NATS and dispatches them to the worker.
type natsConsumer struct {
	js          jetstream.JetStream
	worker      worker.DeliveryWorker
	stream      string
	numWorkers  int
	workerChans []chan jetstream.Msg
	wg          sync.WaitGroup
	metrics     types.Metrics
}

// NewTaskConsumer creates a new TaskConsumer.
func NewTaskConsumer(nc *nats.Conn, w worker.DeliveryWorker, numWorkers int, metrics types.Metrics) (TaskConsumer, error) {
	if nc == nil {
		return nil, fmt.Errorf("nats connection cannot be nil")
	}

	js, err := jetStreamNew(nc)
	if err != nil {
		return nil, err
	}

	return NewTaskConsumerFromJS(js, w, numWorkers, metrics)
}

// NewTaskConsumerFromJS creates a new TaskConsumer using an existing JetStream context.
func NewTaskConsumerFromJS(js jetstream.JetStream, w worker.DeliveryWorker, numWorkers int, metrics types.Metrics) (TaskConsumer, error) {
	if numWorkers <= 0 {
		numWorkers = 16
	}
	if metrics == nil {
		metrics = &types.NoopMetrics{}
	}

	return &natsConsumer{
		js:         js,
		worker:     w,
		stream:     "TRIGGERS",
		numWorkers: numWorkers,
		metrics:    metrics,
	}, nil
}

// Start begins consuming messages. It blocks until the context is cancelled.
func (c *natsConsumer) Start(ctx context.Context) error {
	// Ensure Stream exists
	_, err := c.js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:     c.stream,
		Subjects: []string{"triggers.>"},
		Storage:  jetstream.MemoryStorage,
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
		c.workerChans[i] = make(chan jetstream.Msg, 100)
		c.wg.Add(1)
		go c.workerLoop(ctx, i)
	}

	// Consume messages
	cc, err := consumer.Consume(func(msg jetstream.Msg) {
		c.dispatch(msg)
	})
	if err != nil {
		return fmt.Errorf("failed to start consumer: %w", err)
	}
	defer cc.Stop()

	log.Printf("Trigger Consumer started with %d workers, waiting for messages...", c.numWorkers)

	<-ctx.Done()

	log.Println("Stopping Trigger Consumer...")
	cc.Stop()

	for _, ch := range c.workerChans {
		close(ch)
	}
	c.wg.Wait()
	return nil
}

func (c *natsConsumer) dispatch(msg jetstream.Msg) {
	var task types.DeliveryTask
	if err := json.Unmarshal(msg.Data(), &task); err != nil {
		log.Printf("[Error] Invalid payload in dispatch: %v", err)
		c.metrics.IncConsumeFailure("unknown", "unknown", "unmarshal_error")
		msg.Term()
		return
	}

	h := fnv.New32a()
	h.Write([]byte(task.Collection))
	h.Write([]byte(task.DocKey))
	hash := h.Sum32()
	workerIdx := int(hash % uint32(c.numWorkers))

	c.workerChans[workerIdx] <- msg
}

func (c *natsConsumer) workerLoop(ctx context.Context, id int) {
	defer c.wg.Done()

	for msg := range c.workerChans[id] {
		if err := c.processMsg(ctx, msg); err != nil {
			if types.IsFatal(err) {
				log.Printf("[Error] [Worker %d] Fatal error processing message: %v. Terminating.", id, err)
				msg.Term()
				continue
			}
			log.Printf("[Error] [Worker %d] Failed to process message: %v", id, err)

			md, metaErr := msg.Metadata()
			if metaErr != nil {
				log.Printf("[Error] Failed to get message metadata: %v", metaErr)
				msg.Nak()
				continue
			}

			var task types.DeliveryTask
			if jsonErr := json.Unmarshal(msg.Data(), &task); jsonErr != nil {
				log.Printf("[Error] Invalid payload for retry check: %v", jsonErr)
				msg.Term()
				continue
			}

			maxAttempts := task.RetryPolicy.MaxAttempts
			if maxAttempts == 0 {
				maxAttempts = 3
			}

			if int(md.NumDelivered) >= maxAttempts {
				log.Printf("[Error] Max attempts (%d) reached for trigger %s. Terminating.", maxAttempts, task.TriggerID)
				msg.Term()
				continue
			}

			attempt := int(md.NumDelivered)
			initialBackoff := time.Duration(task.RetryPolicy.InitialBackoff)
			if initialBackoff == 0 {
				initialBackoff = 1 * time.Second
			}

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

func (c *natsConsumer) processMsg(ctx context.Context, msg jetstream.Msg) error {
	start := time.Now()
	var task types.DeliveryTask
	if err := json.Unmarshal(msg.Data(), &task); err != nil {
		c.metrics.IncConsumeFailure("unknown", "unknown", "unmarshal_error")
		return fmt.Errorf("invalid payload: %w", err)
	}

	log.Printf("[Info] Processing trigger task: %s", task.TriggerID)

	timeout := time.Duration(task.Timeout)
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	taskCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	err := c.worker.ProcessTask(taskCtx, &task)
	if err != nil {
		c.metrics.IncConsumeFailure(task.Tenant, task.Collection, err.Error())
	} else {
		c.metrics.IncConsumeSuccess(task.Tenant, task.Collection, task.SubjectHashed)
	}
	c.metrics.ObserveConsumeLatency(task.Tenant, task.Collection, time.Since(start))
	return err
}
