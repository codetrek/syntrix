package trigger

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/codetrek/syntrix/internal/auth"

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
		var task DeliveryTask
		err := json.NewDecoder(r.Body).Decode(&task)
		assert.NoError(t, err)
		assert.Equal(t, "trig-1", task.TriggerID)

		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// 2. Setup Worker
	worker := NewDeliveryWorker(nil)

	// 3. Create Task
	task := &DeliveryTask{
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

	worker := NewDeliveryWorker(nil)

	task := &DeliveryTask{
		TriggerID: "trig-1",
		URL:       server.URL,
	}

	err := worker.ProcessTask(context.Background(), task)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "webhook failed with status: 500")
}

func TestDeliveryWorker_ProcessTask_WithToken(t *testing.T) {
	// 1. Setup Token Service
	key, _ := auth.GeneratePrivateKey()
	tokenService, err := auth.NewTokenService(key, time.Hour, time.Hour, time.Minute)
	assert.NoError(t, err)

	// 2. Setup Mock Server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		task := DeliveryTask{}
		err := json.NewDecoder(r.Body).Decode(&task)
		assert.NoError(t, err)

		// Verify Pre-Issued Token
		assert.NotEmpty(t, task.PreIssuedToken)

		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// 3. Setup Worker
	worker := NewDeliveryWorker(tokenService)

	// 4. Create Task
	task := &DeliveryTask{
		TriggerID: "trig-1",
		URL:       server.URL,
	}

	// 5. Execute
	err = worker.ProcessTask(context.Background(), task)
	assert.NoError(t, err)
}
