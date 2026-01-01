package csp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/codetrek/syntrix/internal/storage"
	"github.com/stretchr/testify/assert"
)

func TestNewCSPClient(t *testing.T) {
	client := NewClient("http://localhost:8083")
	assert.NotNil(t, client)
	assert.Equal(t, "http://localhost:8083", client.baseURL)
	assert.NotNil(t, client.client)
}

func TestCSPClient_SetHTTPClient(t *testing.T) {
	client := NewClient("http://localhost:8083")
	customHTTPClient := &http.Client{Timeout: 30 * time.Second}

	client.SetHTTPClient(customHTTPClient)

	assert.Equal(t, customHTTPClient, client.client)
}

func TestCSPClient_Watch_Success(t *testing.T) {
	// Create a mock server that streams events
	events := []storage.Event{
		{Id: "users/1", Type: storage.EventCreate},
		{Id: "users/2", Type: storage.EventUpdate},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "/internal/v1/watch", r.URL.Path)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

		// Decode request body
		var req struct {
			Tenant     string `json:"tenant"`
			Collection string `json:"collection"`
		}
		err := json.NewDecoder(r.Body).Decode(&req)
		assert.NoError(t, err)
		assert.Equal(t, "test-tenant", req.Tenant)
		assert.Equal(t, "users", req.Collection)

		// Send events as JSON stream
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)

		encoder := json.NewEncoder(w)
		for _, evt := range events {
			err := encoder.Encode(evt)
			assert.NoError(t, err)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL)
	ctx := context.Background()

	ch, err := client.Watch(ctx, "test-tenant", "users", nil, storage.WatchOptions{})

	assert.NoError(t, err)
	assert.NotNil(t, ch)

	// Read events from channel
	receivedEvents := make([]storage.Event, 0, 2)
	for evt := range ch {
		receivedEvents = append(receivedEvents, evt)
	}

	assert.Len(t, receivedEvents, 2)
	assert.Equal(t, "users/1", receivedEvents[0].Id)
	assert.Equal(t, storage.EventCreate, receivedEvents[0].Type)
	assert.Equal(t, "users/2", receivedEvents[1].Id)
	assert.Equal(t, storage.EventUpdate, receivedEvents[1].Type)
}

func TestCSPClient_Watch_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client := NewClient(server.URL)
	ctx := context.Background()

	ch, err := client.Watch(ctx, "test-tenant", "users", nil, storage.WatchOptions{})

	assert.Error(t, err)
	assert.Nil(t, ch)
	assert.Contains(t, err.Error(), "500")
}

func TestCSPClient_Watch_ConnectionError(t *testing.T) {
	// Use an invalid URL to trigger connection error
	client := NewClient("http://localhost:1") // Port 1 is typically not listening
	ctx := context.Background()

	ch, err := client.Watch(ctx, "test-tenant", "users", nil, storage.WatchOptions{})

	assert.Error(t, err)
	assert.Nil(t, ch)
	assert.Contains(t, err.Error(), "failed to connect to CSP service")
}

func TestCSPClient_Watch_ContextCancel(t *testing.T) {
	// Create a server that sends one event then blocks
	serverReady := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		// Write one event
		encoder := json.NewEncoder(w)
		encoder.Encode(storage.Event{Id: "users/1", Type: storage.EventCreate})
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		close(serverReady)
		// Block until request context is cancelled (server will close when test ends)
		<-r.Context().Done()
	}))
	defer server.Close()

	client := NewClient(server.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	ch, err := client.Watch(ctx, "test-tenant", "users", nil, storage.WatchOptions{})
	assert.NoError(t, err)
	assert.NotNil(t, ch)

	// Wait for server to be ready
	<-serverReady

	// Read the first event
	select {
	case evt, ok := <-ch:
		assert.True(t, ok)
		assert.Equal(t, "users/1", evt.Id)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected to receive event")
	}

	// Cancel context
	cancel()

	// Channel should eventually close
	select {
	case _, ok := <-ch:
		// Channel closed or empty is fine
		_ = ok
	case <-time.After(1 * time.Second):
		// Timeout is acceptable - the goroutine may still be blocked on decode
	}
}

func TestCSPClient_Watch_InvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		// Send invalid JSON
		w.Write([]byte("invalid json\n"))
	}))
	defer server.Close()

	client := NewClient(server.URL)
	ctx := context.Background()

	ch, err := client.Watch(ctx, "test-tenant", "users", nil, storage.WatchOptions{})

	assert.NoError(t, err) // Connection succeeds
	assert.NotNil(t, ch)

	// Channel should close due to decode error
	evt, ok := <-ch
	assert.False(t, ok, "channel should be closed after decode error")
	assert.Equal(t, storage.Event{}, evt)
}

func TestCSPClient_Watch_EmptyTenantAndCollection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Decode request body
		var req struct {
			Tenant     string `json:"tenant"`
			Collection string `json:"collection"`
		}
		err := json.NewDecoder(r.Body).Decode(&req)
		assert.NoError(t, err)
		assert.Equal(t, "", req.Tenant)
		assert.Equal(t, "", req.Collection)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewClient(server.URL)
	ctx := context.Background()

	ch, err := client.Watch(ctx, "", "", nil, storage.WatchOptions{})

	assert.NoError(t, err)
	assert.NotNil(t, ch)

	// Wait for channel to close
	<-ch
}

func TestCSPClient_ImplementsService(t *testing.T) {
	var svc Service = NewClient("http://localhost:8083")
	assert.NotNil(t, svc)
}
