package query

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"syntrix/internal/storage"

	"github.com/stretchr/testify/assert"
)

func TestClient_GetDocument(t *testing.T) {
	expectedDoc := &storage.Document{Id: "test/1", Data: map[string]interface{}{"foo": "bar"}}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/internal/v1/document/get", r.URL.Path)

		var req map[string]string
		json.NewDecoder(r.Body).Decode(&req)
		assert.Equal(t, "test/1", req["path"])

		json.NewEncoder(w).Encode(expectedDoc)
	}))
	defer ts.Close()

	client := NewClient(ts.URL)
	doc, err := client.GetDocument(context.Background(), "test/1")
	assert.NoError(t, err)
	assert.Equal(t, expectedDoc, doc)
}

func TestClient_CreateDocument(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/internal/v1/document/create", r.URL.Path)
		w.WriteHeader(http.StatusCreated)
	}))
	defer ts.Close()

	client := NewClient(ts.URL)
	doc := &storage.Document{Id: "test/1"}
	err := client.CreateDocument(context.Background(), doc)
	assert.NoError(t, err)
}

func TestClient_WatchCollection(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/internal/v1/watch", r.URL.Path)

		// Send two events then close
		evt1 := storage.Event{Type: "create", Path: "test/1"}
		evt2 := storage.Event{Type: "update", Path: "test/1"}

		encoder := json.NewEncoder(w)
		encoder.Encode(evt1)
		w.(http.Flusher).Flush()

		encoder.Encode(evt2)
		w.(http.Flusher).Flush()
	}))
	defer ts.Close()

	client := NewClient(ts.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	stream, err := client.WatchCollection(ctx, "test")
	assert.NoError(t, err)

	var events []storage.Event
	for evt := range stream {
		events = append(events, evt)
	}

	assert.Len(t, events, 2)
	assert.Equal(t, storage.EventCreate, events[0].Type)
	assert.Equal(t, storage.EventUpdate, events[1].Type)
}

func TestClient_ErrorHandling(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	client := NewClient(ts.URL)
	_, err := client.GetDocument(context.Background(), "test/1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected status code: 500")
}
