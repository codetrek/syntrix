package worker

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/codetrek/syntrix/internal/identity"
	"github.com/codetrek/syntrix/internal/trigger/types"
)

// HTTPClientOptions configures the HTTP client.
type HTTPClientOptions struct {
	Timeout time.Duration
}

// HTTPWorker handles the execution of delivery tasks via HTTP.
type HTTPWorker struct {
	client  *http.Client
	auth    identity.AuthN
	secrets SecretProvider
	metrics types.Metrics
}

// NewDeliveryWorker creates a new HTTPWorker.
func NewDeliveryWorker(auth identity.AuthN, secrets SecretProvider, opts HTTPClientOptions, metrics types.Metrics) DeliveryWorker {
	timeout := opts.Timeout
	if timeout == 0 {
		timeout = types.DefaultHTTPTimeout
	}
	if metrics == nil {
		metrics = &types.NoopMetrics{}
	}
	return &HTTPWorker{
		client: &http.Client{
			Timeout: timeout,
		},
		auth:    auth,
		secrets: secrets,
		metrics: metrics,
	}
}

// ProcessTask executes a single delivery task.
func (w *HTTPWorker) ProcessTask(ctx context.Context, task *types.DeliveryTask) error {
	start := time.Now()
	// Add System Token
	if w.auth != nil {
		token, err := w.auth.GenerateSystemToken("trigger-worker")
		if err != nil {
			w.metrics.IncDeliveryFailure(task.Tenant, task.Collection, 0, false)
			return fmt.Errorf("failed to generate system token: %w", err)
		}
		task.PreIssuedToken = token
	}

	payload, err := json.Marshal(task)
	if err != nil {
		w.metrics.IncDeliveryFailure(task.Tenant, task.Collection, 0, true)
		return fmt.Errorf("failed to marshal task: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", task.URL, bytes.NewReader(payload))
	if err != nil {
		w.metrics.IncDeliveryFailure(task.Tenant, task.Collection, 0, true)
		return fmt.Errorf("failed to create request: %w", err)
	}

	// Add Headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Syntrix-Trigger-Service/1.0")
	for k, v := range task.Headers {
		req.Header.Set(k, v)
	}

	// Add Signature (only if SecretsRef is configured)
	if task.SecretsRef != "" {
		if w.secrets == nil {
			log.Printf("[Warning] SecretsRef %s specified but no SecretProvider configured", task.SecretsRef)
			w.metrics.IncDeliveryFailure(task.Tenant, task.Collection, 0, true)
			return &types.FatalError{Err: fmt.Errorf("no secret provider configured for SecretsRef %s", task.SecretsRef)}
		}
		secret, err := w.secrets.GetSecret(ctx, task.SecretsRef)
		if err != nil {
			// If we can't get the secret, should we fail fatally or retry?
			// Probably retry, as it might be a temporary issue with secret store.
			w.metrics.IncDeliveryFailure(task.Tenant, task.Collection, 0, false)
			return fmt.Errorf("failed to resolve secret %s: %w", task.SecretsRef, err)
		}
		timestamp := time.Now().Unix()
		signature := w.signPayload(payload, secret, timestamp)
		req.Header.Set("X-Syntrix-Signature", signature)
	}
	// If SecretsRef is empty, skip signature header entirely (webhook may not require it)

	resp, err := w.client.Do(req)
	if err != nil {
		w.metrics.IncDeliveryFailure(task.Tenant, task.Collection, 0, false)
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		w.metrics.IncDeliverySuccess(task.Tenant, task.Collection)
		w.metrics.ObserveDeliveryLatency(task.Tenant, task.Collection, time.Since(start))
		return nil
	}

	fatal := resp.StatusCode >= 400 && resp.StatusCode < 500
	w.metrics.IncDeliveryFailure(task.Tenant, task.Collection, resp.StatusCode, fatal)

	// 4xx errors are fatal, 5xx are retryable.
	if fatal {
		return &types.FatalError{Err: fmt.Errorf("webhook failed with status: %d", resp.StatusCode)}
	}

	return fmt.Errorf("webhook failed with status: %d", resp.StatusCode)
}

func (w *HTTPWorker) signPayload(body []byte, secret string, timestamp int64) string {
	// Signature format: t={ts},v1={hex(hmac)}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(fmt.Sprintf("%d.", timestamp)))
	mac.Write(body)
	sig := hex.EncodeToString(mac.Sum(nil))
	return fmt.Sprintf("t=%d,v1=%s", timestamp, sig)
}
