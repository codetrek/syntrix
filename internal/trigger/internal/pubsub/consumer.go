package pubsub

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/codetrek/syntrix/internal/trigger/internal/worker"
	"github.com/codetrek/syntrix/internal/trigger/types"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// DefaultChannelBufferSize is the default buffer size for worker channels.
const DefaultChannelBufferSize = 100

// natsConsumer consumes delivery tasks from NATS and dispatches them to the worker.
type natsConsumer struct {
	js             jetstream.JetStream
	worker         worker.DeliveryWorker
	stream         string
	numWorkers     int
	channelBufSize int
	workerChans    []chan jetstream.Msg
	wg             sync.WaitGroup
	metrics        types.Metrics

	// Shutdown coordination
	closing         atomic.Bool  // Marks closing state
	inFlightCount   atomic.Int32 // Count of messages currently in dispatch()
	drainTimeout    time.Duration
	shutdownTimeout time.Duration
}

// ConsumerOption configures the consumer.
type ConsumerOption func(*natsConsumer)

// WithChannelBufferSize sets the buffer size for worker channels.
func WithChannelBufferSize(size int) ConsumerOption {
	return func(c *natsConsumer) {
		if size > 0 {
			c.channelBufSize = size
		}
	}
}

// WithDrainTimeout sets the drain timeout for graceful shutdown.
// This is the maximum time to wait for in-flight dispatch() calls to complete.
func WithDrainTimeout(d time.Duration) ConsumerOption {
	return func(c *natsConsumer) {
		if d > 0 {
			c.drainTimeout = d
		}
	}
}

// WithShutdownTimeout sets the overall shutdown timeout.
// This is the maximum time to wait for workers to finish processing.
func WithShutdownTimeout(d time.Duration) ConsumerOption {
	return func(c *natsConsumer) {
		if d > 0 {
			c.shutdownTimeout = d
		}
	}
}

// NewTaskConsumer creates a new TaskConsumer.
func NewTaskConsumer(nc *nats.Conn, w worker.DeliveryWorker, streamName string, numWorkers int, metrics types.Metrics, opts ...ConsumerOption) (TaskConsumer, error) {
	if nc == nil {
		return nil, fmt.Errorf("nats connection cannot be nil")
	}

	js, err := jetStreamNew(nc)
	if err != nil {
		return nil, err
	}

	return NewTaskConsumerFromJS(js, w, streamName, numWorkers, metrics, opts...)
}

// NewTaskConsumerFromJS creates a new TaskConsumer using an existing JetStream context.
func NewTaskConsumerFromJS(js jetstream.JetStream, w worker.DeliveryWorker, streamName string, numWorkers int, metrics types.Metrics, opts ...ConsumerOption) (TaskConsumer, error) {
	if numWorkers <= 0 {
		numWorkers = 16
	}
	if metrics == nil {
		metrics = &types.NoopMetrics{}
	}
	if streamName == "" {
		streamName = "TRIGGERS"
	}

	c := &natsConsumer{
		js:              js,
		worker:          w,
		stream:          streamName,
		numWorkers:      numWorkers,
		channelBufSize:  DefaultChannelBufferSize,
		metrics:         metrics,
		drainTimeout:    types.DefaultDrainTimeout,
		shutdownTimeout: types.DefaultShutdownTimeout,
	}

	for _, opt := range opts {
		opt(c)
	}

	return c, nil
}

// Start begins consuming messages. It blocks until the context is cancelled.
func (c *natsConsumer) Start(ctx context.Context) error {
	// Ensure Stream exists
	_, err := c.js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:     c.stream,
		Subjects: []string{fmt.Sprintf("%s.>", c.stream)},
		Storage:  jetstream.MemoryStorage,
	})
	if err != nil {
		return fmt.Errorf("failed to ensure stream: %w", err)
	}

	// Create Consumer
	consumer, err := c.js.CreateOrUpdateConsumer(ctx, c.stream, jetstream.ConsumerConfig{
		Durable:       "TriggerDeliveryWorker",
		AckPolicy:     jetstream.AckExplicitPolicy,
		FilterSubject: fmt.Sprintf("%s.>", c.stream),
	})
	if err != nil {
		return fmt.Errorf("failed to create consumer: %w", err)
	}

	// Initialize Worker Pool
	c.workerChans = make([]chan jetstream.Msg, c.numWorkers)
	for i := 0; i < c.numWorkers; i++ {
		c.workerChans[i] = make(chan jetstream.Msg, c.channelBufSize)
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

	// Phase 1: Stop accepting new messages
	log.Println("[Info] Stopping Trigger Consumer...")
	c.closing.Store(true)
	cc.Stop()

	// Phase 2: Wait for in-flight dispatches to complete
	drainCtx, drainCancel := context.WithTimeout(context.Background(), c.drainTimeout)
	defer drainCancel()
	c.waitForDrain(drainCtx)

	// Phase 3: Close worker channels
	for _, ch := range c.workerChans {
		close(ch)
	}

	// Phase 4: Wait for workers with timeout
	done := make(chan struct{})
	go func() {
		c.wg.Wait()
		close(done)
	}()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), c.shutdownTimeout)
	defer shutdownCancel()

	select {
	case <-done:
		log.Println("[Info] All workers stopped gracefully")
	case <-shutdownCtx.Done():
		log.Printf("[Warn] Shutdown timeout exceeded, some workers may still be running")
	}

	return nil
}

// waitForDrain waits for all in-flight dispatch() calls to complete.
func (c *natsConsumer) waitForDrain(ctx context.Context) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			remaining := c.inFlightCount.Load()
			if remaining > 0 {
				log.Printf("[Warn] Drain timeout, %d messages still in-flight", remaining)
			}
			return
		case <-ticker.C:
			if c.inFlightCount.Load() == 0 {
				log.Println("[Info] All in-flight messages drained")
				return
			}
		}
	}
}

func (c *natsConsumer) dispatch(msg jetstream.Msg) {
	// Track in-flight count for graceful shutdown
	c.inFlightCount.Add(1)
	defer c.inFlightCount.Add(-1)

	// Check if consumer is closing - NAK message for redelivery
	if c.closing.Load() {
		log.Printf("[Warn] Consumer is closing, NAK message for redelivery")
		msg.Nak()
		return
	}

	var task types.DeliveryTask
	if err := json.Unmarshal(msg.Data(), &task); err != nil {
		log.Printf("[Error] Invalid payload in dispatch: %v", err)
		c.metrics.IncConsumeFailure("unknown", "unknown", "unmarshal_error")
		msg.Term()
		return
	}

	h := fnv.New32a()
	h.Write([]byte(task.Collection))
	h.Write([]byte(task.DocumentID))
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
		timeout = types.DefaultTaskTimeout
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
