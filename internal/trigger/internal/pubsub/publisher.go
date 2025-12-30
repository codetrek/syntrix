package pubsub

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/codetrek/syntrix/internal/trigger/types"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// natsPublisher implements TaskPublisher using NATS JetStream.
type natsPublisher struct {
	js      jetstream.JetStream
	metrics types.Metrics
	prefix  string
}

func NewTaskPublisher(nc *nats.Conn, streamName string, metrics types.Metrics) (TaskPublisher, error) {
	if nc == nil {
		return nil, fmt.Errorf("nats connection cannot be nil")
	}
	js, err := jetStreamNew(nc)
	if err != nil {
		return nil, err
	}

	// Ensure stream exists
	if err := EnsureStream(js, streamName); err != nil {
		return nil, fmt.Errorf("failed to ensure stream: %w", err)
	}

	return NewTaskPublisherFromJS(js, streamName, metrics), nil
}

func EnsureStream(js jetstream.JetStream, streamName string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if streamName == "" {
		streamName = "TRIGGERS"
	}
	subjects := []string{fmt.Sprintf("%s.>", streamName)}

	_, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:     streamName,
		Subjects: subjects,
		Storage:  jetstream.MemoryStorage,
	})
	return err
}

func NewTaskPublisherFromJS(js jetstream.JetStream, streamName string, metrics types.Metrics) TaskPublisher {
	if metrics == nil {
		metrics = &types.NoopMetrics{}
	}
	if streamName == "" {
		streamName = "TRIGGERS"
	}
	return &natsPublisher{js: js, metrics: metrics, prefix: streamName}
}

func (p *natsPublisher) Publish(ctx context.Context, task *types.DeliveryTask) error {
	start := time.Now()
	// Subject format: <streamName>.<tenant>.<collection>.<documentId>
	// DocumentID is base64url encoded to ensure safety.
	encodedDocumentID := base64.URLEncoding.EncodeToString([]byte(task.DocumentID))

	subject := fmt.Sprintf("%s.%s.%s.%s", p.prefix, task.Tenant, task.Collection, encodedDocumentID)

	// NATS subject length limit is usually 64KB, but for performance and practical reasons,
	// we might want to limit it. The requirement says "if length > 1024".
	if len(subject) > 1024 {
		hash := sha256.Sum256([]byte(subject))
		// Truncate to 32 chars (16 bytes hex)
		hashStr := hex.EncodeToString(hash[:16])
		subject = fmt.Sprintf("%s.hashed.%s", p.prefix, hashStr)
		task.SubjectHashed = true
	}

	data, err := json.Marshal(task)
	if err != nil {
		p.metrics.IncPublishFailure(task.Tenant, task.Collection, "marshal_error")
		return err
	}

	_, err = p.js.Publish(ctx, subject, data, jetstream.WithExpectStream(p.prefix), jetstream.WithRetryAttempts(3))
	if err != nil {
		p.metrics.IncPublishFailure(task.Tenant, task.Collection, err.Error())
		return err
	}

	p.metrics.IncPublishSuccess(task.Tenant, task.Collection, task.SubjectHashed)
	p.metrics.ObservePublishLatency(task.Tenant, task.Collection, time.Since(start))
	return nil
}

// Close releases resources held by the publisher.
// The publisher does not own the NATS connection, so this is a no-op.
func (p *natsPublisher) Close() error {
	return nil
}
