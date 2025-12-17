package trigger

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Worker defines the interface for processing delivery tasks.
type Worker interface {
	ProcessTask(ctx context.Context, task *DeliveryTask) error
}

// DeliveryWorker handles the execution of delivery tasks.
type DeliveryWorker struct {
	client *http.Client
}

// NewDeliveryWorker creates a new DeliveryWorker.
func NewDeliveryWorker() *DeliveryWorker {
	return &DeliveryWorker{
		client: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

// ProcessTask executes a single delivery task.
func (w *DeliveryWorker) ProcessTask(ctx context.Context, task *DeliveryTask) error {
	payload, err := json.Marshal(task)
	if err != nil {
		return fmt.Errorf("failed to marshal task: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", task.URL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	// Add Headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Syntrix-Trigger-Service/1.0")
	for k, v := range task.Headers {
		req.Header.Set(k, v)
	}

	// Add Signature
	// In a real implementation, we would fetch the secret from a Secret Manager using task.SecretsRef
	secret := "dummy-secret"
	timestamp := time.Now().Unix()
	signature := w.signPayload(payload, secret, timestamp)
	req.Header.Set("X-Syntrix-Signature", signature)

	resp, err := w.client.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}

	// 4xx errors might be fatal, 5xx are retryable.
	// For now, we return error for anything non-2xx to trigger NATS retry.
	return fmt.Errorf("webhook failed with status: %d", resp.StatusCode)
}

func (w *DeliveryWorker) signPayload(body []byte, secret string, timestamp int64) string {
	// Signature format: t={ts},v1={hex(hmac)}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(fmt.Sprintf("%d.", timestamp)))
	mac.Write(body)
	sig := hex.EncodeToString(mac.Sum(nil))
	return fmt.Sprintf("t=%d,v1=%s", timestamp, sig)
}
