package query

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"syntrix/internal/common"
	"syntrix/internal/storage"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestClient_GetDocument(t *testing.T) {
	expectedDoc := common.Document{"id": "1", "collection": "test", "foo": "bar", "version": float64(1)}

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
	doc := common.Document{"id": "1", "collection": "test"}
	err := client.CreateDocument(context.Background(), doc)
	assert.NoError(t, err)
}

func TestClient_WatchCollection(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/internal/v1/watch", r.URL.Path)

		// Send two events then close
		evt1 := storage.Event{Type: "create", Id: "test/1"}
		evt2 := storage.Event{Type: "update", Id: "test/1"}

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

func TestClient_GetDocument_NotFound(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	client := NewClient(ts.URL)
	_, err := client.GetDocument(context.Background(), "test/1")
	assert.ErrorIs(t, err, storage.ErrNotFound)
}

func TestClient_GetDocument_DecodeError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("not-json"))
	}))
	defer ts.Close()

	client := NewClient(ts.URL)
	res, err := client.GetDocument(context.Background(), "test/1")
	assert.Error(t, err)
	assert.Nil(t, res)
}

func TestClient_CreateDocument_BadStatus(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer ts.Close()

	client := NewClient(ts.URL)
	err := client.CreateDocument(context.Background(), common.Document{"collection": "c"})
	assert.Error(t, err)
}

func TestClient_ReplaceDocument_BadStatus(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer ts.Close()

	client := NewClient(ts.URL)
	res, err := client.ReplaceDocument(context.Background(), common.Document{"collection": "c", "id": "1"}, nil)
	assert.Error(t, err)
	assert.Nil(t, res)
}

func TestClient_PatchDocument_Statuses(t *testing.T) {
	t.Run("not found", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer ts.Close()

		client := NewClient(ts.URL)
		res, err := client.PatchDocument(context.Background(), common.Document{"collection": "c", "id": "1"}, nil)
		assert.ErrorIs(t, err, storage.ErrNotFound)
		assert.Nil(t, res)
	})

	t.Run("bad status", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
		}))
		defer ts.Close()

		client := NewClient(ts.URL)
		res, err := client.PatchDocument(context.Background(), common.Document{"collection": "c", "id": "1"}, nil)
		assert.Error(t, err)
		assert.Nil(t, res)
	})
}

func TestClient_DeleteDocument_Statuses(t *testing.T) {
	t.Run("not found", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer ts.Close()

		client := NewClient(ts.URL)
		err := client.DeleteDocument(context.Background(), "c/1")
		assert.ErrorIs(t, err, storage.ErrNotFound)
	})

	t.Run("bad status", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer ts.Close()

		client := NewClient(ts.URL)
		err := client.DeleteDocument(context.Background(), "c/1")
		assert.Error(t, err)
	})
}

func TestClient_ExecuteQuery_StatusError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	client := NewClient(ts.URL)
	res, err := client.ExecuteQuery(context.Background(), storage.Query{Collection: "c"})
	assert.Error(t, err)
	assert.Nil(t, res)
}

func TestClient_WatchCollection_StatusError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer ts.Close()

	client := NewClient(ts.URL)
	res, err := client.WatchCollection(context.Background(), "c")
	assert.Error(t, err)
	assert.Nil(t, res)
}

func TestClient_post_EncodeError(t *testing.T) {
	client := NewClient("http://example.com")
	_, err := client.post(context.Background(), "/x", make(chan int))
	assert.Error(t, err)
}

func TestClient_Post_DoError(t *testing.T) {
	client := NewClient("http://example.com")
	client.httpClient = &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		return nil, assert.AnError
	})}

	_, err := client.post(context.Background(), "/x", map[string]string{"k": "v"})
	assert.Error(t, err)
}

func TestClient_ReplaceDocument_Success(t *testing.T) {
	expected := common.Document{"id": "1", "collection": "c", "v": float64(2)}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/internal/v1/document/replace", r.URL.Path)
		var body map[string]interface{}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Contains(t, body, "data")
		w.WriteHeader(http.StatusOK)
		require.NoError(t, json.NewEncoder(w).Encode(expected))
	}))
	defer ts.Close()

	client := NewClient(ts.URL)
	doc, err := client.ReplaceDocument(context.Background(), expected, nil)
	assert.NoError(t, err)
	assert.Equal(t, expected, doc)
}

func TestClient_PatchDocument_Success(t *testing.T) {
	expected := common.Document{"id": "1", "collection": "c", "v": float64(3)}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/internal/v1/document/patch", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		require.NoError(t, json.NewEncoder(w).Encode(expected))
	}))
	defer ts.Close()

	client := NewClient(ts.URL)
	doc, err := client.PatchDocument(context.Background(), expected, nil)
	assert.NoError(t, err)
	assert.Equal(t, expected, doc)
}

func TestClient_ExecuteQuery_Success(t *testing.T) {
	expected := []common.Document{{"id": "1", "collection": "c"}}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/internal/v1/query/execute", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		require.NoError(t, json.NewEncoder(w).Encode(expected))
	}))
	defer ts.Close()

	client := NewClient(ts.URL)
	res, err := client.ExecuteQuery(context.Background(), storage.Query{Collection: "c"})
	assert.NoError(t, err)
	assert.Equal(t, expected, res)
}

func TestClient_Pull_Success(t *testing.T) {
	expected := storage.ReplicationPullResponse{Checkpoint: 10}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/internal/replication/v1/pull", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		require.NoError(t, json.NewEncoder(w).Encode(expected))
	}))
	defer ts.Close()

	client := NewClient(ts.URL)
	res, err := client.Pull(context.Background(), storage.ReplicationPullRequest{Collection: "c"})
	assert.NoError(t, err)
	assert.Equal(t, &expected, res)
}

func TestClient_Push_Success(t *testing.T) {
	expected := storage.ReplicationPushResponse{Conflicts: []*storage.Document{}}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/internal/replication/v1/push", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		require.NoError(t, json.NewEncoder(w).Encode(expected))
	}))
	defer ts.Close()

	client := NewClient(ts.URL)
	res, err := client.Push(context.Background(), storage.ReplicationPushRequest{Collection: "c"})
	assert.NoError(t, err)
	assert.Equal(t, &expected, res)
}

func TestClient_RunTransaction_Unsupported(t *testing.T) {
	client := NewClient("http://example.com")
	err := client.RunTransaction(context.Background(), func(ctx context.Context, tx Service) error { return nil })
	assert.Error(t, err)
}

func TestClient_WatchCollection_CancelCloses(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/internal/v1/watch", r.URL.Path)
		flusher, ok := w.(http.Flusher)
		require.True(t, ok)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("\n")) // allow client to start scanner
		flusher.Flush()
		<-r.Context().Done()
	}))
	defer ts.Close()

	client := NewClient(ts.URL)
	ctx, cancel := context.WithCancel(context.Background())
	stream, err := client.WatchCollection(ctx, "c")
	require.NoError(t, err)

	cancel()

	select {
	case _, ok := <-stream:
		if ok {
			t.Fatal("expected channel to close on cancel")
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("stream did not close after cancel")
	}
}

func TestClient_WatchCollection_InvalidJSONSkipped(t *testing.T) {
	valid := storage.Event{Type: storage.EventCreate, Id: "c/1"}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/internal/v1/watch", r.URL.Path)
		flusher, ok := w.(http.Flusher)
		require.True(t, ok)
		_, _ = w.Write([]byte("not-json\n"))
		flusher.Flush()
		require.NoError(t, json.NewEncoder(w).Encode(valid))
		flusher.Flush()
	}))
	defer ts.Close()

	client := NewClient(ts.URL)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	stream, err := client.WatchCollection(ctx, "c")
	require.NoError(t, err)

	var events []storage.Event
	for evt := range stream {
		events = append(events, evt)
	}

	require.Len(t, events, 1)
	assert.Equal(t, valid, events[0])
}
