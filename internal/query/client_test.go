package query

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/codetrek/syntrix/internal/storage"
	"github.com/codetrek/syntrix/pkg/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestClient_GetDocument(t *testing.T) {
	expectedDoc := model.Document{"id": "1", "collection": "test", "foo": "bar", "version": float64(1)}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/internal/v1/document/get", r.URL.Path)

		var req map[string]string
		json.NewDecoder(r.Body).Decode(&req)
		assert.Equal(t, "test/1", req["path"])
		assert.Equal(t, "default", req["tenant"])

		json.NewEncoder(w).Encode(expectedDoc)
	}))
	defer ts.Close()

	client := NewClient(ts.URL)
	doc, err := client.GetDocument(context.Background(), "default", "test/1")
	assert.NoError(t, err)
	assert.Equal(t, expectedDoc, doc)
}

func TestClient_CreateDocument(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/internal/v1/document/create", r.URL.Path)
		var req map[string]interface{}
		json.NewDecoder(r.Body).Decode(&req)
		assert.Equal(t, "default", req["tenant"])
		w.WriteHeader(http.StatusCreated)
	}))
	defer ts.Close()

	client := NewClient(ts.URL)
	doc := model.Document{"id": "1", "collection": "test"}
	err := client.CreateDocument(context.Background(), "default", doc)
	assert.NoError(t, err)
}

func TestClient_WatchCollection(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/internal/v1/watch", r.URL.Path)
		var req map[string]string
		json.NewDecoder(r.Body).Decode(&req)
		assert.Equal(t, "default", req["tenant"])

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

	stream, err := client.WatchCollection(ctx, "default", "test")
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
	_, err := client.GetDocument(context.Background(), "default", "test/1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected status code: 500")
}

func TestClient_GetDocument_NotFound(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	client := NewClient(ts.URL)
	_, err := client.GetDocument(context.Background(), "default", "test/1")
	assert.ErrorIs(t, err, model.ErrNotFound)
}

func TestClient_GetDocument_DecodeError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("not-json"))
	}))
	defer ts.Close()

	client := NewClient(ts.URL)
	res, err := client.GetDocument(context.Background(), "default", "test/1")
	assert.Error(t, err)
	assert.Nil(t, res)
}

func TestClient_CreateDocument_BadStatus(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer ts.Close()

	client := NewClient(ts.URL)
	err := client.CreateDocument(context.Background(), "default", model.Document{"collection": "c"})
	assert.Error(t, err)
}

func TestClient_ReplaceDocument_BadStatus(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer ts.Close()

	client := NewClient(ts.URL)
	res, err := client.ReplaceDocument(context.Background(), "default", model.Document{"collection": "c", "id": "1"}, nil)
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
		res, err := client.PatchDocument(context.Background(), "default", model.Document{"collection": "c", "id": "1"}, nil)
		assert.ErrorIs(t, err, model.ErrNotFound)
		assert.Nil(t, res)
	})

	t.Run("bad status", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
		}))
		defer ts.Close()

		client := NewClient(ts.URL)
		res, err := client.PatchDocument(context.Background(), "default", model.Document{"collection": "c", "id": "1"}, nil)
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
		err := client.DeleteDocument(context.Background(), "default", "c/1", nil)
		assert.ErrorIs(t, err, model.ErrNotFound)
	})

	t.Run("bad status", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer ts.Close()

		client := NewClient(ts.URL)
		err := client.DeleteDocument(context.Background(), "default", "c/1", nil)
		assert.Error(t, err)
	})

	t.Run("precondition failed", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusPreconditionFailed)
		}))
		defer ts.Close()

		client := NewClient(ts.URL)
		err := client.DeleteDocument(context.Background(), "default", "c/1", nil)
		assert.ErrorIs(t, err, model.ErrPreconditionFailed)
	})
}

func TestClient_ExecuteQuery_StatusError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	client := NewClient(ts.URL)
	res, err := client.ExecuteQuery(context.Background(), "default", model.Query{Collection: "c"})
	assert.Error(t, err)
	assert.Nil(t, res)
}

func TestClient_WatchCollection_StatusError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer ts.Close()

	client := NewClient(ts.URL)
	res, err := client.WatchCollection(context.Background(), "default", "c")
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
	expected := model.Document{"id": "1", "collection": "c", "v": float64(2)}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/internal/v1/document/replace", r.URL.Path)
		var body map[string]interface{}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Contains(t, body, "data")
		require.Equal(t, "default", body["tenant"])
		w.WriteHeader(http.StatusOK)
		require.NoError(t, json.NewEncoder(w).Encode(expected))
	}))
	defer ts.Close()

	client := NewClient(ts.URL)
	doc, err := client.ReplaceDocument(context.Background(), "default", expected, nil)
	assert.NoError(t, err)
	assert.Equal(t, expected, doc)
}

func TestClient_PatchDocument_Success(t *testing.T) {
	expected := model.Document{"id": "1", "collection": "c", "v": float64(3)}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/internal/v1/document/patch", r.URL.Path)
		var body map[string]interface{}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Equal(t, "default", body["tenant"])
		w.WriteHeader(http.StatusOK)
		require.NoError(t, json.NewEncoder(w).Encode(expected))
	}))
	defer ts.Close()

	client := NewClient(ts.URL)
	doc, err := client.PatchDocument(context.Background(), "default", expected, nil)
	assert.NoError(t, err)
	assert.Equal(t, expected, doc)
}

func TestClient_ExecuteQuery_Success(t *testing.T) {
	expected := []model.Document{{"id": "1", "collection": "c"}}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/internal/v1/query/execute", r.URL.Path)
		var body map[string]interface{}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Equal(t, "default", body["tenant"])
		w.WriteHeader(http.StatusOK)
		require.NoError(t, json.NewEncoder(w).Encode(expected))
	}))
	defer ts.Close()

	client := NewClient(ts.URL)
	res, err := client.ExecuteQuery(context.Background(), "default", model.Query{Collection: "c"})
	assert.NoError(t, err)
	assert.Equal(t, expected, res)
}

func TestClient_Pull_Success(t *testing.T) {
	expected := storage.ReplicationPullResponse{Checkpoint: 10}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/internal/replication/v1/pull", r.URL.Path)
		var body map[string]interface{}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Equal(t, "default", body["tenant"])
		w.WriteHeader(http.StatusOK)
		require.NoError(t, json.NewEncoder(w).Encode(expected))
	}))
	defer ts.Close()

	client := NewClient(ts.URL)
	res, err := client.Pull(context.Background(), "default", storage.ReplicationPullRequest{Collection: "c"})
	assert.NoError(t, err)
	assert.Equal(t, &expected, res)
}

func TestClient_Push_Success(t *testing.T) {
	expected := storage.ReplicationPushResponse{Conflicts: []*storage.Document{}}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/internal/replication/v1/push", r.URL.Path)
		var body map[string]interface{}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Equal(t, "default", body["tenant"])
		w.WriteHeader(http.StatusOK)
		require.NoError(t, json.NewEncoder(w).Encode(expected))
	}))
	defer ts.Close()

	client := NewClient(ts.URL)
	res, err := client.Push(context.Background(), "default", storage.ReplicationPushRequest{Collection: "c"})
	assert.NoError(t, err)
	assert.Equal(t, &expected, res)
}

func TestClient_WatchCollection_CancelCloses(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/internal/v1/watch", r.URL.Path)
		var body map[string]interface{}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Equal(t, "default", body["tenant"])
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
	stream, err := client.WatchCollection(ctx, "default", "c")
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
		var body map[string]interface{}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Equal(t, "default", body["tenant"])
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
	stream, err := client.WatchCollection(ctx, "default", "c")
	require.NoError(t, err)

	var events []storage.Event
	for evt := range stream {
		events = append(events, evt)
	}

	require.Len(t, events, 1)
	assert.Equal(t, valid, events[0])
}

func TestClient_Pull(t *testing.T) {
	expectedResp := &storage.ReplicationPullResponse{
		Documents: []*storage.Document{
			{Id: "1", Collection: "test", Data: map[string]interface{}{"foo": "bar"}},
		},
		Checkpoint: 100,
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/internal/replication/v1/pull", r.URL.Path)
		var body map[string]interface{}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Equal(t, "default", body["tenant"])
		json.NewEncoder(w).Encode(expectedResp)
	}))
	defer ts.Close()

	client := NewClient(ts.URL)
	req := storage.ReplicationPullRequest{
		Checkpoint: 50,
		Limit:      10,
	}
	resp, err := client.Pull(context.Background(), "default", req)
	assert.NoError(t, err)
	assert.Equal(t, expectedResp.Checkpoint, resp.Checkpoint)
	assert.Len(t, resp.Documents, 1)
}

func TestClient_Push(t *testing.T) {
	expectedResp := &storage.ReplicationPushResponse{
		Conflicts: nil,
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/internal/replication/v1/push", r.URL.Path)
		var body map[string]interface{}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Equal(t, "default", body["tenant"])
		json.NewEncoder(w).Encode(expectedResp)
	}))
	defer ts.Close()

	client := NewClient(ts.URL)
	req := storage.ReplicationPushRequest{
		Changes: []storage.ReplicationPushChange{
			{
				Doc: &storage.Document{Id: "1", Collection: "test", Data: map[string]interface{}{"foo": "bar"}},
			},
		},
	}
	resp, err := client.Push(context.Background(), "default", req)
	assert.NoError(t, err)
	assert.Empty(t, resp.Conflicts)
}

func TestClient_Pull_Error(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	client := NewClient(ts.URL)
	req := storage.ReplicationPullRequest{}
	_, err := client.Pull(context.Background(), "default", req)
	assert.Error(t, err)
}

func TestClient_Push_Error(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	client := NewClient(ts.URL)
	req := storage.ReplicationPushRequest{}
	_, err := client.Push(context.Background(), "default", req)
	assert.Error(t, err)
}

func TestClient_NetworkErrors(t *testing.T) {
	// Create a client with a URL that will cause connection failure
	// Using a closed port or invalid host
	client := NewClient("http://localhost:0")

	ctx := context.Background()

	t.Run("GetDocument connection error", func(t *testing.T) {
		_, err := client.GetDocument(ctx, "default", "test/1")
		assert.Error(t, err)
	})

	t.Run("CreateDocument connection error", func(t *testing.T) {
		err := client.CreateDocument(ctx, "default", model.Document{"id": "1"})
		assert.Error(t, err)
	})

	t.Run("ReplaceDocument connection error", func(t *testing.T) {
		_, err := client.ReplaceDocument(ctx, "default", model.Document{"id": "1"}, nil)
		assert.Error(t, err)
	})

	t.Run("PatchDocument connection error", func(t *testing.T) {
		_, err := client.PatchDocument(ctx, "default", model.Document{"id": "1"}, nil)
		assert.Error(t, err)
	})

	t.Run("DeleteDocument connection error", func(t *testing.T) {
		err := client.DeleteDocument(ctx, "default", "test/1", nil)
		assert.Error(t, err)
	})

	t.Run("ExecuteQuery connection error", func(t *testing.T) {
		_, err := client.ExecuteQuery(ctx, "default", model.Query{})
		assert.Error(t, err)
	})

	t.Run("WatchCollection connection error", func(t *testing.T) {
		_, err := client.WatchCollection(ctx, "default", "test")
		assert.Error(t, err)
	})

	t.Run("Pull connection error", func(t *testing.T) {
		_, err := client.Pull(ctx, "default", storage.ReplicationPullRequest{})
		assert.Error(t, err)
	})

	t.Run("Push connection error", func(t *testing.T) {
		_, err := client.Push(ctx, "default", storage.ReplicationPushRequest{})
		assert.Error(t, err)
	})
}

func TestClient_WatchCollection_Errors(t *testing.T) {
	t.Run("Bad Status Code", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer ts.Close()

		client := NewClient(ts.URL)
		_, err := client.WatchCollection(context.Background(), "default", "test")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "unexpected status code: 500")
	})

	t.Run("Invalid URL", func(t *testing.T) {
		// Control character in URL to force NewRequest error
		client := NewClient("http://example.com" + string(byte(0x7f)))
		_, err := client.WatchCollection(context.Background(), "default", "test")
		assert.Error(t, err)
	})
}

func TestClient_Replication_Errors(t *testing.T) {
	t.Run("Pull Bad Status", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
		}))
		defer ts.Close()

		client := NewClient(ts.URL)
		_, err := client.Pull(context.Background(), "default", storage.ReplicationPullRequest{})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "unexpected status code: 400")
	})

	t.Run("Pull Decode Error", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("invalid-json"))
		}))
		defer ts.Close()

		client := NewClient(ts.URL)
		_, err := client.Pull(context.Background(), "default", storage.ReplicationPullRequest{})
		assert.Error(t, err)
	})

	t.Run("Push Bad Status", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer ts.Close()

		client := NewClient(ts.URL)
		_, err := client.Push(context.Background(), "default", storage.ReplicationPushRequest{})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "unexpected status code: 500")
	})

	t.Run("Push Decode Error", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("invalid-json"))
		}))
		defer ts.Close()

		client := NewClient(ts.URL)
		_, err := client.Push(context.Background(), "default", storage.ReplicationPushRequest{})
		assert.Error(t, err)
	})
}

func TestClient_ExecuteQuery_Errors(t *testing.T) {
	t.Run("Bad Status", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
		}))
		defer ts.Close()

		client := NewClient(ts.URL)
		_, err := client.ExecuteQuery(context.Background(), "default", model.Query{})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "unexpected status code: 400")
	})

	t.Run("Decode Error", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("invalid-json"))
		}))
		defer ts.Close()

		client := NewClient(ts.URL)
		_, err := client.ExecuteQuery(context.Background(), "default", model.Query{})
		assert.Error(t, err)
	})
}
