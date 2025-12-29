package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/codetrek/syntrix/internal/storage"
	"github.com/codetrek/syntrix/pkg/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestEngine_ExecuteQuery(t *testing.T) {
	type testCase struct {
		name         string
		query        model.Query
		mockSetup    func(*MockStorageBackend)
		expectedDocs []model.Document
		expectError  bool
	}

	tests := []testCase{
		{
			name: "Success",
			query: model.Query{
				Collection: "test",
				Filters: []model.Filter{
					{Field: "foo", Op: "==", Value: "bar"},
				},
			},
			mockSetup: func(m *MockStorageBackend) {
				storedDocs := []*storage.Document{
					{
						Fullpath:   "test/1",
						Collection: "test",
						Data:       map[string]interface{}{"foo": "bar"},
						Version:    1,
						UpdatedAt:  100,
						CreatedAt:  90,
					},
				}
				m.On("Query", mock.Anything, "default", mock.MatchedBy(func(q model.Query) bool {
					return q.Collection == "test" && q.Filters[0].Value == "bar"
				})).Return(storedDocs, nil)
			},
			expectedDocs: []model.Document{
				{
					"id":         "1",
					"collection": "test",
					"foo":        "bar",
					"version":    int64(1),
					"updatedAt":  int64(100),
					"createdAt":  int64(90),
				},
			},
			expectError: false,
		},
		{
			name: "Storage Error",
			query: model.Query{
				Collection: "test",
			},
			mockSetup: func(m *MockStorageBackend) {
				m.On("Query", mock.Anything, "default", mock.Anything).Return(nil, assert.AnError)
			},
			expectError: true,
		},
		{
			name: "Empty Result",
			query: model.Query{
				Collection: "test",
			},
			mockSetup: func(m *MockStorageBackend) {
				m.On("Query", mock.Anything, "default", mock.Anything).Return([]*storage.Document{}, nil)
			},
			expectedDocs: []model.Document{},
			expectError:  false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mockStorage := new(MockStorageBackend)
			if tc.mockSetup != nil {
				tc.mockSetup(mockStorage)
			}
			engine := New(mockStorage, "http://mock-csp")
			ctx := context.Background()

			docs, err := engine.ExecuteQuery(ctx, "default", tc.query)

			if tc.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, len(tc.expectedDocs), len(docs))
				for i, d := range tc.expectedDocs {
					assert.Equal(t, d, docs[i])
				}
			}
			mockStorage.AssertExpectations(t)
		})
	}
}

func TestEngine_Push_Coverage(t *testing.T) {
	type testCase struct {
		name              string
		req               storage.ReplicationPushRequest
		mockSetup         func(*MockStorageBackend)
		expectedConflicts []*storage.Document
		expectError       bool
	}

	tests := []testCase{
		{
			name: "Conflict (Version Mismatch)",
			req: storage.ReplicationPushRequest{
				Collection: "test",
				Changes: []storage.ReplicationPushChange{
					{
						Doc:         &storage.Document{Id: "test/1", Fullpath: "test/1", Collection: "test", Data: map[string]interface{}{"foo": "bar"}, Version: 2},
						BaseVersion: ptr(int64(1)),
					},
				},
			},
			mockSetup: func(m *MockStorageBackend) {
				// Existing doc has version 3, but we expect base version 1 -> Conflict
				existingDoc := &storage.Document{Id: "test/1", Fullpath: "test/1", Version: 3, Data: map[string]interface{}{"foo": "old"}}
				m.On("Get", mock.Anything, "default", "test/1").Return(existingDoc, nil)
			},
			expectedConflicts: []*storage.Document{
				{Id: "test/1", Fullpath: "test/1", Version: 3, Data: map[string]interface{}{"foo": "old"}},
			},
			expectError: false,
		},
		{
			name: "Delete Success",
			req: storage.ReplicationPushRequest{
				Collection: "test",
				Changes: []storage.ReplicationPushChange{
					{
						Doc:         &storage.Document{Id: "test/1", Fullpath: "test/1", Collection: "test", Deleted: true, Version: 2},
						BaseVersion: ptr(int64(1)),
					},
				},
			},
			mockSetup: func(m *MockStorageBackend) {
				existingDoc := &storage.Document{Id: "test/1", Fullpath: "test/1", Version: 1}
				m.On("Get", mock.Anything, "default", "test/1").Return(existingDoc, nil)
				m.On("Delete", mock.Anything, "default", "test/1", mock.MatchedBy(func(f model.Filters) bool {
					return f[0].Field == "version" && f[0].Value == int64(1)
				})).Return(nil)
			},
			expectedConflicts: nil,
			expectError:       false,
		},
		{
			name: "Delete Conflict (Precondition Failed)",
			req: storage.ReplicationPushRequest{
				Collection: "test",
				Changes: []storage.ReplicationPushChange{
					{
						Doc:         &storage.Document{Id: "test/1", Fullpath: "test/1", Collection: "test", Deleted: true, Version: 2},
						BaseVersion: ptr(int64(1)),
					},
				},
			},
			mockSetup: func(m *MockStorageBackend) {
				existingDoc := &storage.Document{Id: "test/1", Fullpath: "test/1", Version: 1}
				m.On("Get", mock.Anything, "default", "test/1").Return(existingDoc, nil).Once()

				// Delete fails with PreconditionFailed
				m.On("Delete", mock.Anything, "default", "test/1", mock.Anything).Return(model.ErrPreconditionFailed)

				// Fetch latest for conflict
				latestDoc := &storage.Document{Id: "test/1", Fullpath: "test/1", Version: 3}
				m.On("Get", mock.Anything, "default", "test/1").Return(latestDoc, nil).Once()
			},
			expectedConflicts: []*storage.Document{
				{Id: "test/1", Fullpath: "test/1", Version: 3},
			},
			expectError: false,
		},
		{
			name: "Create Conflict (Already Exists)",
			req: storage.ReplicationPushRequest{
				Collection: "test",
				Changes: []storage.ReplicationPushChange{
					{
						Doc: &storage.Document{Id: "test/1", Fullpath: "test/1", Collection: "test", Version: 1},
					},
				},
			},
			mockSetup: func(m *MockStorageBackend) {
				// Get returns NotFound, so we try to Create
				m.On("Get", mock.Anything, "default", "test/1").Return(nil, model.ErrNotFound)
				// Create fails (maybe race condition)
				m.On("Create", mock.Anything, "default", mock.Anything).Return(assert.AnError)
			},
			expectedConflicts: []*storage.Document{
				{Id: "test/1", Fullpath: "test/1", Collection: "test", Version: 1},
			},
			expectError: false,
		},
		{
			name: "Get Error",
			req: storage.ReplicationPushRequest{
				Collection: "test",
				Changes: []storage.ReplicationPushChange{
					{Doc: &storage.Document{Id: "test/1", Fullpath: "test/1"}},
				},
			},
			mockSetup: func(m *MockStorageBackend) {
				m.On("Get", mock.Anything, "default", "test/1").Return(nil, assert.AnError)
			},
			expectError: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mockStorage := new(MockStorageBackend)
			if tc.mockSetup != nil {
				tc.mockSetup(mockStorage)
			}
			engine := New(mockStorage, "http://mock-csp")
			ctx := context.Background()

			resp, err := engine.Push(ctx, "default", tc.req)

			if tc.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, len(tc.expectedConflicts), len(resp.Conflicts))
			}
			mockStorage.AssertExpectations(t)
		})
	}
}

func ptr(i int64) *int64 {
	return &i
}

func TestFlattenStorageDocument_Nil(t *testing.T) {
	res := flattenStorageDocument(nil)
	assert.Nil(t, res)
}

func TestFlattenStorageDocument_Deleted(t *testing.T) {
	doc := &storage.Document{
		Fullpath:   "col/doc1",
		Collection: "col",
		Data:       map[string]interface{}{"foo": "bar"},
		Deleted:    true,
	}
	res := flattenStorageDocument(doc)
	assert.True(t, res["deleted"].(bool))
}

func TestExtractIDFromFullpath_Invalid(t *testing.T) {
	id := extractIDFromFullpath("col")
	assert.Equal(t, "", id)
}

func TestExtractIDFromFullpath_Valid(t *testing.T) {
	id := extractIDFromFullpath("col/doc1")
	assert.Equal(t, "doc1", id)
}

func TestReplaceDocument_StorageError(t *testing.T) {
	mockStorage := new(MockStorageBackend)
	engine := New(mockStorage, "")

	doc := model.Document{"id": "doc1", "collection": "col", "foo": "bar"}

	// Get returns error
	mockStorage.On("Get", mock.Anything, "default", "col/doc1").Return(nil, errors.New("db error"))

	_, err := engine.ReplaceDocument(context.Background(), "default", doc, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "db error")
}

func TestReplaceDocument_CreateError(t *testing.T) {
	mockStorage := new(MockStorageBackend)
	engine := New(mockStorage, "")

	doc := model.Document{"id": "doc1", "collection": "col", "foo": "bar"}

	// Get returns NotFound
	mockStorage.On("Get", mock.Anything, "default", "col/doc1").Return(nil, model.ErrNotFound)
	// Create returns error
	mockStorage.On("Create", mock.Anything, "default", mock.Anything).Return(errors.New("create error"))

	_, err := engine.ReplaceDocument(context.Background(), "default", doc, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "create error")
}

func TestReplaceDocument_UpdateError(t *testing.T) {
	mockStorage := new(MockStorageBackend)
	engine := New(mockStorage, "")

	doc := model.Document{"id": "doc1", "collection": "col", "foo": "bar"}

	// Get returns success
	mockStorage.On("Get", mock.Anything, "default", "col/doc1").Return(&storage.Document{}, nil).Once()
	// Update returns error
	mockStorage.On("Update", mock.Anything, "default", "col/doc1", mock.Anything, mock.Anything).Return(errors.New("update error"))

	_, err := engine.ReplaceDocument(context.Background(), "default", doc, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "update error")
}

func TestReplaceDocument_GetAfterUpdateError(t *testing.T) {
	mockStorage := new(MockStorageBackend)
	engine := New(mockStorage, "")

	doc := model.Document{"id": "doc1", "collection": "col", "foo": "bar"}

	// Get returns success
	mockStorage.On("Get", mock.Anything, "default", "col/doc1").Return(&storage.Document{}, nil).Once()
	// Update returns success
	mockStorage.On("Update", mock.Anything, "default", "col/doc1", mock.Anything, mock.Anything).Return(nil)
	// Get after update returns error
	mockStorage.On("Get", mock.Anything, "default", "col/doc1").Return(nil, errors.New("get error")).Once()

	_, err := engine.ReplaceDocument(context.Background(), "default", doc, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "get error")
}

func TestWatchCollection_RequestError(t *testing.T) {
	mockStorage := new(MockStorageBackend)
	engine := New(mockStorage, "http://invalid-url")

	// Use MockTransport to simulate connection error immediately
	mockTransport := &MockTransport{
		RoundTripFunc: func(req *http.Request) (*http.Response, error) {
			return nil, errors.New("connection refused")
		},
	}
	engine.SetHTTPClient(&http.Client{Transport: mockTransport})

	_, err := engine.WatchCollection(context.Background(), "default", "col")
	assert.Error(t, err)
}

func TestWatchCollection_BadStatus(t *testing.T) {
	mockStorage := new(MockStorageBackend)
	engine := New(mockStorage, "http://mock-csp")

	// Mock HTTP Client
	mockTransport := &MockTransport{
		RoundTripFunc: func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusInternalServerError,
				Body:       http.NoBody,
			}, nil
		},
	}
	engine.SetHTTPClient(&http.Client{Transport: mockTransport})

	_, err := engine.WatchCollection(context.Background(), "default", "col")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "csp watch failed")
}

func TestPull_QueryEmpty(t *testing.T) {
	mockStorage := new(MockStorageBackend)
	engine := New(mockStorage, "")

	mockStorage.On("Query", mock.Anything, "default", mock.Anything).Return(nil, nil)

	resp, err := engine.Pull(context.Background(), "default", storage.ReplicationPullRequest{
		Collection: "col",
		Checkpoint: 100,
		Limit:      10,
	})

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Empty(t, resp.Documents)
	assert.Equal(t, int64(100), resp.Checkpoint)
}

func TestPush_DeleteNotFound(t *testing.T) {
	mockStorage := new(MockStorageBackend)
	engine := New(mockStorage, "")

	req := storage.ReplicationPushRequest{
		Collection: "col",
		Changes: []storage.ReplicationPushChange{
			{
				Doc: &storage.Document{
					Fullpath: "col/doc1",
					Deleted:  true,
				},
			},
		},
	}

	mockStorage.On("Get", mock.Anything, "default", "col/doc1").Return(&storage.Document{Version: 1}, nil).Once()
	mockStorage.On("Get", mock.Anything, "default", "col/doc1").Return(nil, model.ErrNotFound)
	mockStorage.On("Delete", mock.Anything, "default", "col/doc1", mock.Anything).Return(model.ErrNotFound)

	resp, err := engine.Push(context.Background(), "default", req)
	assert.NoError(t, err)
	assert.Empty(t, resp.Conflicts)
}

func TestPush_UpdateConflict(t *testing.T) {
	mockStorage := new(MockStorageBackend)
	engine := New(mockStorage, "")

	baseVer := int64(1)
	req := storage.ReplicationPushRequest{
		Collection: "col",
		Changes: []storage.ReplicationPushChange{
			{
				Doc: &storage.Document{
					Fullpath: "col/doc1",
					Data:     map[string]interface{}{"foo": "bar"},
				},
				BaseVersion: &baseVer,
			},
		},
	}

	// Get returns existing doc with version 2 (conflict)
	mockStorage.On("Get", mock.Anything, "default", "col/doc1").Return(&storage.Document{
		Fullpath: "col/doc1",
		Version:  2,
		Data:     map[string]interface{}{"foo": "baz"},
	}, nil)

	resp, err := engine.Push(context.Background(), "default", req)
	assert.NoError(t, err)
	assert.Len(t, resp.Conflicts, 1)
	assert.Equal(t, int64(2), resp.Conflicts[0].Version)
}

func TestPush_UpdatePreconditionFailed(t *testing.T) {
	mockStorage := new(MockStorageBackend)
	engine := New(mockStorage, "")

	baseVer := int64(1)
	req := storage.ReplicationPushRequest{
		Collection: "col",
		Changes: []storage.ReplicationPushChange{
			{
				Doc: &storage.Document{
					Fullpath: "col/doc1",
					Data:     map[string]interface{}{"foo": "bar"},
				},
				BaseVersion: &baseVer,
			},
		},
	}

	// Get returns existing doc with version 1 (match)
	mockStorage.On("Get", mock.Anything, "default", "col/doc1").Return(&storage.Document{
		Fullpath: "col/doc1",
		Version:  1,
	}, nil).Once()

	// Update fails with PreconditionFailed (race condition)
	mockStorage.On("Update", mock.Anything, "default", "col/doc1", mock.Anything, mock.Anything).Return(model.ErrPreconditionFailed)

	// Fetch latest for conflict
	mockStorage.On("Get", mock.Anything, "default", "col/doc1").Return(&storage.Document{
		Fullpath: "col/doc1",
		Version:  2,
	}, nil).Once()

	resp, err := engine.Push(context.Background(), "default", req)
	assert.NoError(t, err)
	assert.Len(t, resp.Conflicts, 1)
	assert.Equal(t, int64(2), resp.Conflicts[0].Version)
}

// TestPush_EmptyFullpathWithIDInData tests Push when Fullpath is empty but ID is in Data
func TestPush_EmptyFullpathWithIDInData(t *testing.T) {
	mockStorage := new(MockStorageBackend)
	engine := New(mockStorage, "")

	req := storage.ReplicationPushRequest{
		Collection: "col",
		Changes: []storage.ReplicationPushChange{
			{
				Doc: &storage.Document{
					Fullpath: "", // Empty fullpath
					Data:     map[string]interface{}{"id": "doc1", "foo": "bar"},
				},
			},
		},
	}

	// Should construct fullpath as col/doc1 from Data["id"]
	mockStorage.On("Get", mock.Anything, "default", "col/doc1").Return(nil, model.ErrNotFound)
	mockStorage.On("Create", mock.Anything, "default", mock.MatchedBy(func(d *storage.Document) bool {
		return d.Fullpath == "col/doc1"
	})).Return(nil)

	resp, err := engine.Push(context.Background(), "default", req)
	assert.NoError(t, err)
	assert.Empty(t, resp.Conflicts)
	mockStorage.AssertExpectations(t)
}

// TestPush_CreateConflict tests Push when Create fails (document already exists race)
func TestPush_CreateConflict(t *testing.T) {
	mockStorage := new(MockStorageBackend)
	engine := New(mockStorage, "")

	req := storage.ReplicationPushRequest{
		Collection: "col",
		Changes: []storage.ReplicationPushChange{
			{
				Doc: &storage.Document{
					Fullpath: "col/doc1",
					Data:     map[string]interface{}{"foo": "bar"},
				},
			},
		},
	}

	// Document doesn't exist initially
	mockStorage.On("Get", mock.Anything, "default", "col/doc1").Return(nil, model.ErrNotFound)
	// But Create fails (race condition - someone else created it)
	mockStorage.On("Create", mock.Anything, "default", mock.Anything).Return(model.ErrExists)

	resp, err := engine.Push(context.Background(), "default", req)
	assert.NoError(t, err)
	assert.Len(t, resp.Conflicts, 1)
	mockStorage.AssertExpectations(t)
}

// TestPush_DeletePreconditionFailed tests Push delete with version mismatch
func TestPush_DeletePreconditionFailed(t *testing.T) {
	mockStorage := new(MockStorageBackend)
	engine := New(mockStorage, "")

	baseVer := int64(1)
	req := storage.ReplicationPushRequest{
		Collection: "col",
		Changes: []storage.ReplicationPushChange{
			{
				Doc: &storage.Document{
					Fullpath: "col/doc1",
					Deleted:  true,
				},
				BaseVersion: &baseVer,
			},
		},
	}

	// Document exists with matching version
	mockStorage.On("Get", mock.Anything, "default", "col/doc1").Return(&storage.Document{
		Fullpath: "col/doc1",
		Version:  1,
	}, nil).Once()

	// Delete fails with PreconditionFailed
	mockStorage.On("Delete", mock.Anything, "default", "col/doc1", mock.Anything).Return(model.ErrPreconditionFailed)

	// Fetch latest for conflict
	mockStorage.On("Get", mock.Anything, "default", "col/doc1").Return(&storage.Document{
		Fullpath: "col/doc1",
		Version:  2,
	}, nil).Once()

	resp, err := engine.Push(context.Background(), "default", req)
	assert.NoError(t, err)
	assert.Len(t, resp.Conflicts, 1)
	assert.Equal(t, int64(2), resp.Conflicts[0].Version)
	mockStorage.AssertExpectations(t)
}

// TestPush_DeleteStorageError tests Push delete with storage error
func TestPush_DeleteStorageError(t *testing.T) {
	mockStorage := new(MockStorageBackend)
	engine := New(mockStorage, "")

	req := storage.ReplicationPushRequest{
		Collection: "col",
		Changes: []storage.ReplicationPushChange{
			{
				Doc: &storage.Document{
					Fullpath: "col/doc1",
					Deleted:  true,
				},
			},
		},
	}

	// Document exists
	mockStorage.On("Get", mock.Anything, "default", "col/doc1").Return(&storage.Document{
		Fullpath: "col/doc1",
		Version:  1,
	}, nil)

	// Delete fails with unexpected error
	mockStorage.On("Delete", mock.Anything, "default", "col/doc1", mock.Anything).Return(errors.New("storage error"))

	resp, err := engine.Push(context.Background(), "default", req)
	assert.Error(t, err)
	assert.Nil(t, resp)
	mockStorage.AssertExpectations(t)
}

// TestPush_UpdateStorageError tests Push update with storage error
func TestPush_UpdateStorageError(t *testing.T) {
	mockStorage := new(MockStorageBackend)
	engine := New(mockStorage, "")

	req := storage.ReplicationPushRequest{
		Collection: "col",
		Changes: []storage.ReplicationPushChange{
			{
				Doc: &storage.Document{
					Fullpath: "col/doc1",
					Data:     map[string]interface{}{"foo": "bar"},
				},
			},
		},
	}

	// Document exists
	mockStorage.On("Get", mock.Anything, "default", "col/doc1").Return(&storage.Document{
		Fullpath: "col/doc1",
		Version:  1,
	}, nil)

	// Update fails with unexpected error
	mockStorage.On("Update", mock.Anything, "default", "col/doc1", mock.Anything, mock.Anything).Return(errors.New("storage error"))

	resp, err := engine.Push(context.Background(), "default", req)
	assert.Error(t, err)
	assert.Nil(t, resp)
	mockStorage.AssertExpectations(t)
}

// TestPush_GetStorageError tests Push when Get returns unexpected error
func TestPush_GetStorageError(t *testing.T) {
	mockStorage := new(MockStorageBackend)
	engine := New(mockStorage, "")

	req := storage.ReplicationPushRequest{
		Collection: "col",
		Changes: []storage.ReplicationPushChange{
			{
				Doc: &storage.Document{
					Fullpath: "col/doc1",
					Data:     map[string]interface{}{"foo": "bar"},
				},
			},
		},
	}

	// Get fails with unexpected error
	mockStorage.On("Get", mock.Anything, "default", "col/doc1").Return(nil, errors.New("connection error"))

	resp, err := engine.Push(context.Background(), "default", req)
	assert.Error(t, err)
	assert.Nil(t, resp)
	mockStorage.AssertExpectations(t)
}

// MockTransport for HTTP Client
type MockTransport struct {
	RoundTripFunc func(req *http.Request) (*http.Response, error)
}

func (m *MockTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return m.RoundTripFunc(req)
}

// ==================================================
// DeleteDocument Tests
// ==================================================

func TestDeleteDocument_Success(t *testing.T) {
	mockStorage := new(MockStorageBackend)
	engine := New(mockStorage, "")

	mockStorage.On("Delete", mock.Anything, "default", "col/doc1", model.Filters(nil)).Return(nil)

	err := engine.DeleteDocument(context.Background(), "default", "col/doc1", nil)
	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
}

func TestDeleteDocument_WithPredicate(t *testing.T) {
	mockStorage := new(MockStorageBackend)
	engine := New(mockStorage, "")

	pred := model.Filters{{Field: "version", Op: "==", Value: int64(1)}}
	mockStorage.On("Delete", mock.Anything, "default", "col/doc1", pred).Return(nil)

	err := engine.DeleteDocument(context.Background(), "default", "col/doc1", pred)
	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
}

func TestDeleteDocument_NotFound(t *testing.T) {
	mockStorage := new(MockStorageBackend)
	engine := New(mockStorage, "")

	mockStorage.On("Delete", mock.Anything, "default", "col/doc1", model.Filters(nil)).Return(model.ErrNotFound)

	err := engine.DeleteDocument(context.Background(), "default", "col/doc1", nil)
	assert.ErrorIs(t, err, model.ErrNotFound)
	mockStorage.AssertExpectations(t)
}

func TestDeleteDocument_PreconditionFailed(t *testing.T) {
	mockStorage := new(MockStorageBackend)
	engine := New(mockStorage, "")

	pred := model.Filters{{Field: "version", Op: "==", Value: int64(1)}}
	mockStorage.On("Delete", mock.Anything, "default", "col/doc1", pred).Return(model.ErrPreconditionFailed)

	err := engine.DeleteDocument(context.Background(), "default", "col/doc1", pred)
	assert.ErrorIs(t, err, model.ErrPreconditionFailed)
	mockStorage.AssertExpectations(t)
}

func TestDeleteDocument_StorageError(t *testing.T) {
	mockStorage := new(MockStorageBackend)
	engine := New(mockStorage, "")

	storageErr := errors.New("storage error")
	mockStorage.On("Delete", mock.Anything, "default", "col/doc1", model.Filters(nil)).Return(storageErr)

	err := engine.DeleteDocument(context.Background(), "default", "col/doc1", nil)
	assert.Error(t, err)
	assert.Equal(t, storageErr, err)
	mockStorage.AssertExpectations(t)
}

// ==================================================
// WatchCollection Success Path Tests
// ==================================================

func TestWatchCollection_Success(t *testing.T) {
	mockStorage := new(MockStorageBackend)
	engine := New(mockStorage, "http://mock-csp")

	// Pre-encode events to a buffer - use Id field since Fullpath has json:"-"
	var buf bytes.Buffer
	events := []storage.Event{
		{Type: storage.EventCreate, Document: &storage.Document{Id: "doc1", Collection: "col"}},
		{Type: storage.EventUpdate, Document: &storage.Document{Id: "doc2", Collection: "col"}},
	}
	for _, evt := range events {
		json.NewEncoder(&buf).Encode(evt)
	}
	bodyData := buf.Bytes()

	mockTransport := &MockTransport{
		RoundTripFunc: func(req *http.Request) (*http.Response, error) {
			assert.Equal(t, "POST", req.Method)
			assert.Contains(t, req.URL.Path, "/internal/v1/watch")
			// Use a fresh buffer with the pre-encoded data
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewReader(bodyData)),
			}, nil
		},
	}
	engine.SetHTTPClient(&http.Client{Transport: mockTransport})

	ch, err := engine.WatchCollection(context.Background(), "default", "col")
	assert.NoError(t, err)
	assert.NotNil(t, ch)

	// Read events from channel
	received := make([]storage.Event, 0, 2)
	for evt := range ch {
		received = append(received, evt)
	}

	assert.Len(t, received, 2)
	assert.Equal(t, "doc1", received[0].Document.Id)
	assert.Equal(t, "col", received[0].Document.Collection)
	assert.Equal(t, "doc2", received[1].Document.Id)
	assert.Equal(t, "col", received[1].Document.Collection)
}

func TestWatchCollection_ContextCancel(t *testing.T) {
	mockStorage := new(MockStorageBackend)
	engine := New(mockStorage, "http://mock-csp")

	// Use a channel to track when the pipe should send data
	sendData := make(chan struct{})
	pipeCloseRequested := make(chan struct{})
	pr, pw := io.Pipe()

	// Close pipe when test signals
	go func() {
		<-pipeCloseRequested
		pw.Close()
	}()

	mockTransport := &MockTransport{
		RoundTripFunc: func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       pr,
			}, nil
		},
	}
	engine.SetHTTPClient(&http.Client{Transport: mockTransport})

	ctx, cancel := context.WithCancel(context.Background())
	ch, err := engine.WatchCollection(ctx, "default", "col")
	assert.NoError(t, err)
	assert.NotNil(t, ch)

	// Start sending data in goroutine - use Id field since Fullpath has json:"-"
	go func() {
		// Wait for signal or timeout
		select {
		case <-sendData:
			evt := storage.Event{Type: storage.EventCreate, Document: &storage.Document{Id: "doc1", Collection: "col"}}
			json.NewEncoder(pw).Encode(evt)
		case <-time.After(time.Second):
		}
	}()
	close(sendData)

	// Read the event
	select {
	case received := <-ch:
		assert.Equal(t, "doc1", received.Document.Id)
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for event")
	}

	// Cancel context and close pipe to unblock decoder
	cancel()
	close(pipeCloseRequested)

	// Channel should eventually close
	select {
	case _, ok := <-ch:
		assert.False(t, ok, "channel should be closed")
	case <-time.After(time.Second):
		// This is acceptable - context cancel doesn't close read immediately
	}
}

func TestWatchCollection_DecodeError(t *testing.T) {
	mockStorage := new(MockStorageBackend)
	engine := New(mockStorage, "http://mock-csp")

	// Create response with invalid JSON
	mockTransport := &MockTransport{
		RoundTripFunc: func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewBufferString("invalid json\n")),
			}, nil
		},
	}
	engine.SetHTTPClient(&http.Client{Transport: mockTransport})

	ch, err := engine.WatchCollection(context.Background(), "default", "col")
	assert.NoError(t, err)
	assert.NotNil(t, ch)

	// Channel should close after decode error
	select {
	case _, ok := <-ch:
		assert.False(t, ok, "channel should close on decode error")
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for channel close")
	}
}

// ==================================================
// Additional Edge Case Tests
// ==================================================

func TestGetDocument_CustomTenant(t *testing.T) {
	mockStorage := new(MockStorageBackend)
	engine := New(mockStorage, "")

	doc := &storage.Document{
		Fullpath:   "col/doc1",
		Collection: "col",
		Data:       map[string]interface{}{"foo": "bar"},
		Version:    1,
	}
	mockStorage.On("Get", mock.Anything, "custom-tenant", "col/doc1").Return(doc, nil)

	result, err := engine.GetDocument(context.Background(), "custom-tenant", "col/doc1")
	assert.NoError(t, err)
	assert.Equal(t, "bar", result["foo"])
	mockStorage.AssertExpectations(t)
}

func TestCreateDocument_CustomTenant(t *testing.T) {
	mockStorage := new(MockStorageBackend)
	engine := New(mockStorage, "")

	mockStorage.On("Create", mock.Anything, "custom-tenant", mock.Anything).Return(nil)

	doc := model.Document{"collection": "col", "id": "doc1", "foo": "bar"}
	err := engine.CreateDocument(context.Background(), "custom-tenant", doc)
	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
}

func TestReplaceDocument_CustomTenant(t *testing.T) {
	mockStorage := new(MockStorageBackend)
	engine := New(mockStorage, "")

	// ReplaceDocument calls Get first to check if doc exists
	mockStorage.On("Get", mock.Anything, "custom-tenant", "col/doc1").Return(&storage.Document{
		Fullpath:   "col/doc1",
		Collection: "col",
		Version:    1,
	}, nil).Once()
	mockStorage.On("Update", mock.Anything, "custom-tenant", "col/doc1", mock.Anything, model.Filters(nil)).Return(nil)
	// After update, Get is called again to return updated doc
	mockStorage.On("Get", mock.Anything, "custom-tenant", "col/doc1").Return(&storage.Document{
		Fullpath:   "col/doc1",
		Collection: "col",
		Data:       map[string]interface{}{"foo": "bar"},
		Version:    2,
	}, nil).Once()

	doc := model.Document{"collection": "col", "id": "doc1", "foo": "bar"}
	_, err := engine.ReplaceDocument(context.Background(), "custom-tenant", doc, nil)
	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
}

func TestPatchDocument_CustomTenant(t *testing.T) {
	mockStorage := new(MockStorageBackend)
	engine := New(mockStorage, "")

	// PatchDocument calls Patch then Get
	mockStorage.On("Patch", mock.Anything, "custom-tenant", "col/doc1", mock.Anything, model.Filters(nil)).Return(nil)
	mockStorage.On("Get", mock.Anything, "custom-tenant", "col/doc1").Return(&storage.Document{
		Fullpath:   "col/doc1",
		Collection: "col",
		Data:       map[string]interface{}{"foo": "bar", "baz": "qux"},
		Version:    2,
	}, nil)

	doc := model.Document{"collection": "col", "id": "doc1", "baz": "qux"}
	_, err := engine.PatchDocument(context.Background(), "custom-tenant", doc, nil)
	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
}

func TestDeleteDocument_CustomTenant(t *testing.T) {
	mockStorage := new(MockStorageBackend)
	engine := New(mockStorage, "")

	mockStorage.On("Delete", mock.Anything, "custom-tenant", "col/doc1", model.Filters(nil)).Return(nil)

	err := engine.DeleteDocument(context.Background(), "custom-tenant", "col/doc1", nil)
	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
}

func TestExecuteQuery_CustomTenant(t *testing.T) {
	mockStorage := new(MockStorageBackend)
	engine := New(mockStorage, "")

	storedDocs := []*storage.Document{
		{
			Fullpath:   "col/doc1",
			Collection: "col",
			Data:       map[string]interface{}{"foo": "bar"},
			Version:    1,
		},
	}
	mockStorage.On("Query", mock.Anything, "custom-tenant", mock.Anything).Return(storedDocs, nil)

	query := model.Query{Collection: "col"}
	docs, err := engine.ExecuteQuery(context.Background(), "custom-tenant", query)
	assert.NoError(t, err)
	assert.Len(t, docs, 1)
	mockStorage.AssertExpectations(t)
}

func TestWatchCollection_CustomTenant(t *testing.T) {
	mockStorage := new(MockStorageBackend)
	engine := New(mockStorage, "http://mock-csp")

	mockTransport := &MockTransport{
		RoundTripFunc: func(req *http.Request) (*http.Response, error) {
			// Verify tenant is passed in request body
			var body map[string]string
			json.NewDecoder(req.Body).Decode(&body)
			assert.Equal(t, "custom-tenant", body["tenant"])
			assert.Equal(t, "col", body["collection"])
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewBuffer(nil)),
			}, nil
		},
	}
	engine.SetHTTPClient(&http.Client{Transport: mockTransport})

	ch, err := engine.WatchCollection(context.Background(), "custom-tenant", "col")
	assert.NoError(t, err)
	assert.NotNil(t, ch)
	// Wait for channel to close (empty body)
	<-ch
}

func TestPull_CustomTenant(t *testing.T) {
	mockStorage := new(MockStorageBackend)
	engine := New(mockStorage, "")

	storedDocs := []*storage.Document{
		{
			Fullpath:   "col/doc1",
			Collection: "col",
			Data:       map[string]interface{}{"foo": "bar"},
			Version:    1,
			UpdatedAt:  200,
		},
	}
	mockStorage.On("Query", mock.Anything, "custom-tenant", mock.Anything).Return(storedDocs, nil)

	req := storage.ReplicationPullRequest{
		Collection: "col",
		Checkpoint: 100,
		Limit:      10,
	}
	resp, err := engine.Pull(context.Background(), "custom-tenant", req)
	assert.NoError(t, err)
	assert.Len(t, resp.Documents, 1)
	mockStorage.AssertExpectations(t)
}

func TestPush_CustomTenant(t *testing.T) {
	mockStorage := new(MockStorageBackend)
	engine := New(mockStorage, "")

	req := storage.ReplicationPushRequest{
		Collection: "col",
		Changes: []storage.ReplicationPushChange{
			{
				Doc: &storage.Document{
					Fullpath: "col/doc1",
					Data:     map[string]interface{}{"foo": "bar"},
				},
			},
		},
	}

	mockStorage.On("Get", mock.Anything, "custom-tenant", "col/doc1").Return(nil, model.ErrNotFound)
	mockStorage.On("Create", mock.Anything, "custom-tenant", mock.Anything).Return(nil)

	resp, err := engine.Push(context.Background(), "custom-tenant", req)
	assert.NoError(t, err)
	assert.Empty(t, resp.Conflicts)
	mockStorage.AssertExpectations(t)
}
