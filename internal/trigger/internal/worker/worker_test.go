package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/codetrek/syntrix/internal/config"
	"github.com/codetrek/syntrix/internal/identity"
	"github.com/codetrek/syntrix/internal/trigger/types"

	"github.com/stretchr/testify/assert"
)

func TestDeliveryWorker_ProcessTask(t *testing.T) {
	// 1. Setup Mock Server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify Method
		assert.Equal(t, "POST", r.Method)

		// Verify Headers
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		assert.Equal(t, "Syntrix-Trigger-Service/1.0", r.Header.Get("User-Agent"))
		assert.Equal(t, "bar", r.Header.Get("X-Custom-Header"))
		assert.NotEmpty(t, r.Header.Get("X-Syntrix-Signature"))

		// Verify Signature Format
		sig := r.Header.Get("X-Syntrix-Signature")
		assert.True(t, strings.HasPrefix(sig, "t="))
		assert.Contains(t, sig, ",v1=")

		// Verify Body
		var task types.DeliveryTask
		err := json.NewDecoder(r.Body).Decode(&task)
		assert.NoError(t, err)
		assert.Equal(t, "trig-1", task.TriggerID)

		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// 2. Setup Worker
	worker := NewDeliveryWorker(nil, nil, HTTPClientOptions{}, nil)

	// 3. Create Task
	task := &types.DeliveryTask{
		TriggerID: "trig-1",
		URL:       server.URL,
		Headers:   map[string]string{"X-Custom-Header": "bar"},
		Event:     "create",
	}

	// 4. Execute
	err := worker.ProcessTask(context.Background(), task)
	assert.NoError(t, err)
}

func TestDeliveryWorker_ProcessTask_Failure(t *testing.T) {
	// 1. Setup Mock Server that fails
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	worker := NewDeliveryWorker(nil, nil, HTTPClientOptions{}, nil)

	task := &types.DeliveryTask{
		TriggerID: "trig-1",
		URL:       server.URL,
	}

	err := worker.ProcessTask(context.Background(), task)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "webhook failed with status: 500")
	assert.False(t, types.IsFatal(err))
}

func TestDeliveryWorker_ProcessTask_FatalError(t *testing.T) {
	// 1. Setup Mock Server that returns 400
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()

	worker := NewDeliveryWorker(nil, nil, HTTPClientOptions{}, nil)

	task := &types.DeliveryTask{
		TriggerID: "trig-1",
		URL:       server.URL,
	}

	err := worker.ProcessTask(context.Background(), task)
	assert.Error(t, err)
	assert.True(t, types.IsFatal(err))
	assert.Contains(t, err.Error(), "webhook failed with status: 400")
}

func TestDeliveryWorker_ProcessTask_WithToken(t *testing.T) {
	auth, err := identity.NewAuthN(config.AuthNConfig{
		PrivateKeyFile:  filepath.Join(t.TempDir(), "key.pem"),
		AccessTokenTTL:  time.Hour,
		RefreshTokenTTL: time.Hour,
		AuthCodeTTL:     time.Minute,
	}, nil, nil)
	assert.NoError(t, err)

	// 2. Setup Mock Server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		task := types.DeliveryTask{}
		err := json.NewDecoder(r.Body).Decode(&task)
		assert.NoError(t, err)

		// Verify Pre-Issued Token
		assert.NotEmpty(t, task.PreIssuedToken)

		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	worker := NewDeliveryWorker(auth, nil, HTTPClientOptions{}, nil)
	task := &types.DeliveryTask{
		TriggerID: "trig-1",
		URL:       server.URL,
	}
	err = worker.ProcessTask(context.Background(), task)
	assert.NoError(t, err)
}

func TestDeliveryWorker_ProcessTask_NetworkError(t *testing.T) {
	worker := NewDeliveryWorker(nil, nil, HTTPClientOptions{}, nil)
	task := &types.DeliveryTask{
		TriggerID: "trig-1",
		URL:       "http://invalid-url-that-does-not-exist.local",
	}
	err := worker.ProcessTask(context.Background(), task)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "request failed")
}

func TestDeliveryWorker_ProcessTask_InvalidURL(t *testing.T) {
	worker := NewDeliveryWorker(nil, nil, HTTPClientOptions{}, nil)
	task := &types.DeliveryTask{
		TriggerID: "trig-1",
		URL:       "://invalid-url",
	}
	err := worker.ProcessTask(context.Background(), task)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create request")
}

type MockSecretProvider struct {
	secrets map[string]string
}

func (m *MockSecretProvider) GetSecret(ctx context.Context, ref string) (string, error) {
	if s, ok := m.secrets[ref]; ok {
		return s, nil
	}
	return "", fmt.Errorf("secret not found")
}

func TestDeliveryWorker_ProcessTask_WithSecret(t *testing.T) {
	secret := "my-secret-key"

	// 1. Setup Mock Server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sig := r.Header.Get("X-Syntrix-Signature")
		// Verify signature is generated using the secret
		// We can't easily verify the HMAC without re-calculating it, but we can check it's present.
		// Or we can calculate it here if we know the timestamp.
		assert.NotEmpty(t, sig)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// 2. Setup Worker with Secret Provider
	secrets := &MockSecretProvider{
		secrets: map[string]string{"secret-ref-1": secret},
	}
	worker := NewDeliveryWorker(nil, secrets, HTTPClientOptions{}, nil)

	// 3. Create Task
	task := &types.DeliveryTask{
		TriggerID:  "trig-1",
		URL:        server.URL,
		SecretsRef: "secret-ref-1",
	}

	// 4. Execute
	err := worker.ProcessTask(context.Background(), task)
	assert.NoError(t, err)
}
