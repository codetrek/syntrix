package query

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/codetrek/syntrix/internal/storage"
	"github.com/codetrek/syntrix/pkg/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestEngine_GetDocument(t *testing.T) {
	mockStorage := new(MockStorageBackend)
	engine := NewEngine(mockStorage, "http://mock-csp")
	ctx := context.Background()

	storedDoc := &storage.Document{Fullpath: "test/1", Collection: "test", Data: map[string]interface{}{"foo": "bar"}, Version: 2, UpdatedAt: 5, CreatedAt: 4}
	mockStorage.On("Get", ctx, "test/1").Return(storedDoc, nil)

	expectedDoc := model.Document{"foo": "bar", "id": "1", "collection": "test", "version": int64(2), "updatedAt": int64(5), "createdAt": int64(4)}

	doc, err := engine.GetDocument(ctx, "test/1")
	assert.NoError(t, err)
	assert.Equal(t, expectedDoc, doc)
	mockStorage.AssertExpectations(t)
}

func TestEngine_CreateDocument(t *testing.T) {
	mockStorage := new(MockStorageBackend)
	engine := NewEngine(mockStorage, "http://mock-csp")
	ctx := context.Background()

	doc := model.Document{
		"id":         "1",
		"collection": "test",
		"foo":        "bar",
		"version":    int64(99),
		"updatedAt":  int64(1234),
		"createdAt":  int64(5678),
	}
	mockStorage.On("Create", ctx, mock.MatchedBy(func(d *storage.Document) bool {
		_, hasVersion := d.Data["version"]
		_, hasUpdated := d.Data["updatedAt"]
		_, hasCreated := d.Data["createdAt"]
		_, hasCollection := d.Data["collection"]

		return d.Fullpath == "test/1" && d.Collection == "test" && d.Data["foo"] == "bar" && !hasVersion && !hasUpdated && !hasCreated && !hasCollection
	})).Return(nil)

	err := engine.CreateDocument(ctx, doc)
	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
}

func TestEngine_CreateDocument_StorageError(t *testing.T) {
	mockStorage := new(MockStorageBackend)
	engine := NewEngine(mockStorage, "http://mock-csp")
	ctx := context.Background()

	doc := model.Document{"id": "1", "collection": "test", "foo": "bar"}
	mockStorage.On("Create", ctx, mock.AnythingOfType("*storage.Document")).Return(assert.AnError)

	err := engine.CreateDocument(ctx, doc)
	assert.Error(t, err)
	mockStorage.AssertExpectations(t)
}

func TestEngine_CreateDocument_InvalidInputs(t *testing.T) {
	engine := NewEngine(new(MockStorageBackend), "http://mock-csp")
	ctx := context.Background()

	t.Run("nil doc", func(t *testing.T) {
		err := engine.CreateDocument(ctx, nil)
		assert.Error(t, err)
	})

	t.Run("missing collection", func(t *testing.T) {
		err := engine.CreateDocument(ctx, model.Document{"id": "1"})
		assert.Error(t, err)
	})
}

func TestEngine_ReplaceDocument_Create(t *testing.T) {
	mockStorage := new(MockStorageBackend)
	engine := NewEngine(mockStorage, "http://mock-csp")
	ctx := context.Background()

	path := "test/1"
	collection := "test"
	data := model.Document{"foo": "bar", "version": int64(99), "updatedAt": int64(1), "createdAt": int64(2), "collection": "ignored"}
	data.SetID("1")
	data.SetCollection(collection)

	// Simulate Not Found -> Create
	mockStorage.On("Get", ctx, path).Return(nil, model.ErrNotFound)
	mockStorage.On("Create", ctx, mock.MatchedBy(func(d *storage.Document) bool {
		_, hasVersion := d.Data["version"]
		_, hasUpdated := d.Data["updatedAt"]
		_, hasCreated := d.Data["createdAt"]
		_, hasCollection := d.Data["collection"]

		return d.Fullpath == path && d.Collection == collection && d.Data["foo"] == "bar" && !hasVersion && !hasUpdated && !hasCreated && !hasCollection
	})).Return(nil)

	doc, err := engine.ReplaceDocument(ctx, data, model.Filters{})
	assert.NoError(t, err)
	assert.Equal(t, "1", doc.GetID())
	assert.Equal(t, collection, doc.GetCollection())
	assert.Equal(t, "bar", doc["foo"])
	mockStorage.AssertExpectations(t)
}

func TestEngine_ReplaceDocument_CreateError(t *testing.T) {
	mockStorage := new(MockStorageBackend)
	engine := NewEngine(mockStorage, "http://mock-csp")
	ctx := context.Background()

	path := "test/1"
	collection := "test"
	data := model.Document{"foo": "bar"}
	data.SetID("1")
	data.SetCollection(collection)

	mockStorage.On("Get", ctx, path).Return(nil, model.ErrNotFound)
	mockStorage.On("Create", ctx, mock.AnythingOfType("*storage.Document")).Return(assert.AnError)

	doc, err := engine.ReplaceDocument(ctx, data, model.Filters{})
	assert.Error(t, err)
	assert.Nil(t, doc)
	mockStorage.AssertExpectations(t)
}

func TestEngine_ReplaceDocument_Update(t *testing.T) {
	mockStorage := new(MockStorageBackend)
	engine := NewEngine(mockStorage, "http://mock-csp")
	ctx := context.Background()

	path := "test/1"
	collection := "test"
	data := model.Document{"foo": "new", "version": int64(99), "updatedAt": int64(1), "createdAt": int64(2), "collection": "ignored"}
	data.SetID("1")
	data.SetCollection(collection)
	existingDoc := &storage.Document{Id: path, Version: 1, Data: map[string]interface{}{"foo": "old"}}

	// Simulate Found -> Update -> Get
	mockStorage.On("Get", ctx, path).Return(existingDoc, nil).Once()
	expectedUpdate := map[string]interface{}{"foo": "new", "id": "1"}
	mockStorage.On("Update", ctx, path, expectedUpdate, model.Filters{}).Return(nil)
	mockStorage.On("Get", ctx, path).Return(&storage.Document{Id: path, Version: 2, Data: expectedUpdate}, nil).Once()

	doc, err := engine.ReplaceDocument(ctx, data, model.Filters{})
	assert.NoError(t, err)
	assert.Equal(t, "new", doc["foo"])
	assert.Equal(t, int64(2), doc["version"])
	mockStorage.AssertExpectations(t)
}

func TestEngine_ReplaceDocument_UpdateError(t *testing.T) {
	mockStorage := new(MockStorageBackend)
	engine := NewEngine(mockStorage, "http://mock-csp")
	ctx := context.Background()

	path := "test/1"
	collection := "test"
	data := model.Document{"foo": "new"}
	data.SetID("1")
	data.SetCollection(collection)

	mockStorage.On("Get", ctx, path).Return(&storage.Document{Id: path, Version: 1, Data: map[string]interface{}{"foo": "old"}}, nil).Once()
	mockStorage.On("Update", ctx, path, map[string]interface{}(data), model.Filters{}).Return(assert.AnError)

	doc, err := engine.ReplaceDocument(ctx, data, model.Filters{})
	assert.Error(t, err)
	assert.Nil(t, doc)
	mockStorage.AssertExpectations(t)
}

func TestEngine_ReplaceDocument_InvalidInputs(t *testing.T) {
	engine := NewEngine(new(MockStorageBackend), "http://mock-csp")
	ctx := context.Background()

	t.Run("nil doc", func(t *testing.T) {
		res, err := engine.ReplaceDocument(ctx, nil, nil)
		assert.Error(t, err)
		assert.Nil(t, res)
	})

	t.Run("missing collection", func(t *testing.T) {
		res, err := engine.ReplaceDocument(ctx, model.Document{"id": "1"}, nil)
		assert.Error(t, err)
		assert.Nil(t, res)
	})

	t.Run("missing id", func(t *testing.T) {
		res, err := engine.ReplaceDocument(ctx, model.Document{"collection": "c"}, nil)
		assert.Error(t, err)
		assert.Nil(t, res)
	})
}

func TestEngine_PatchDocument(t *testing.T) {
	mockStorage := new(MockStorageBackend)
	engine := NewEngine(mockStorage, "http://mock-csp")
	ctx := context.Background()

	path := "test/1"
	patchData := model.Document{"bar": "baz", "version": int64(2), "updatedAt": int64(1), "createdAt": int64(2), "collection": "ignored"}
	patchData.SetID("1")
	patchData.SetCollection("test")

	patchFields := map[string]interface{}{"bar": "baz"}

	// Expect Patch call with user fields only (system fields stripped)
	mockStorage.On("Patch", ctx, path, patchFields, model.Filters{}).Return(nil).Once()

	expectedMergedData := map[string]interface{}{"foo": "old", "bar": "baz"}
	mockStorage.On("Get", ctx, path).Return(&storage.Document{Id: path, Version: 2, Data: expectedMergedData}, nil).Once()

	doc, err := engine.PatchDocument(ctx, patchData, model.Filters{})
	assert.NoError(t, err)
	assert.Equal(t, expectedMergedData["foo"], doc["foo"])
	assert.Equal(t, expectedMergedData["bar"], doc["bar"])
	mockStorage.AssertExpectations(t)
}

func TestEngine_PatchDocument_Error(t *testing.T) {
	mockStorage := new(MockStorageBackend)
	engine := NewEngine(mockStorage, "http://mock-csp")
	ctx := context.Background()

	path := "test/1"
	patchData := model.Document{"bar": "baz"}
	patchData.SetID("1")
	patchData.SetCollection("test")

	mockStorage.On("Patch", ctx, path, map[string]interface{}{"bar": "baz"}, model.Filters{}).Return(assert.AnError).Once()

	doc, err := engine.PatchDocument(ctx, patchData, model.Filters{})
	assert.Error(t, err)
	assert.Nil(t, doc)
	mockStorage.AssertExpectations(t)
}

func TestEngine_PatchDocument_InvalidInputs(t *testing.T) {
	engine := NewEngine(new(MockStorageBackend), "http://mock-csp")
	ctx := context.Background()

	t.Run("nil doc", func(t *testing.T) {
		res, err := engine.PatchDocument(ctx, nil, nil)
		assert.Error(t, err)
		assert.Nil(t, res)
	})

	t.Run("missing collection", func(t *testing.T) {
		res, err := engine.PatchDocument(ctx, model.Document{"id": "1"}, nil)
		assert.Error(t, err)
		assert.Nil(t, res)
	})

	t.Run("missing id", func(t *testing.T) {
		res, err := engine.PatchDocument(ctx, model.Document{"collection": "c"}, nil)
		assert.Error(t, err)
		assert.Nil(t, res)
	})
}

func TestEngine_ReplaceDocument_IfMatch(t *testing.T) {
	mockStorage := new(MockStorageBackend)
	engine := NewEngine(mockStorage, "http://mock-csp")
	ctx := context.Background()

	path := "test/1"
	collection := "test"
	data := model.Document{"foo": "new"}
	data.SetID("1")
	data.SetCollection(collection)
	existingDoc := &storage.Document{Id: path, Version: 1, Data: map[string]interface{}{"foo": "old"}}

	filters := model.Filters{
		{Field: "version", Op: "==", Value: int64(1)},
	}

	// Simulate Found -> Update -> Get
	mockStorage.On("Get", ctx, path).Return(existingDoc, nil).Once()
	mockStorage.On("Update", ctx, path, map[string]interface{}(data), filters).Return(nil)
	mockStorage.On("Get", ctx, path).Return(&storage.Document{Id: path, Version: 2, Data: data}, nil).Once()

	doc, err := engine.ReplaceDocument(ctx, data, filters)
	assert.NoError(t, err)
	assert.Equal(t, "new", doc["foo"])
	assert.Equal(t, int64(2), doc["version"])
	mockStorage.AssertExpectations(t)
}

func TestEngine_PatchDocument_IfMatch(t *testing.T) {
	mockStorage := new(MockStorageBackend)
	engine := NewEngine(mockStorage, "http://mock-csp")
	ctx := context.Background()

	path := "test/1"
	patchData := model.Document{"bar": "baz", "version": int64(2), "updatedAt": int64(1), "createdAt": int64(2), "collection": "ignored"}
	patchData.SetID("1")
	patchData.SetCollection("test")

	filters := model.Filters{
		{Field: "foo", Op: "==", Value: "old"},
	}

	// Expect Patch call with filters
	patchFields := map[string]interface{}{"bar": "baz"}
	mockStorage.On("Patch", ctx, path, patchFields, filters).Return(nil).Once()

	expectedMergedData := map[string]interface{}{"foo": "old", "bar": "baz"}
	mockStorage.On("Get", ctx, path).Return(&storage.Document{Id: path, Version: 2, Data: expectedMergedData}, nil).Once()

	doc, err := engine.PatchDocument(ctx, patchData, filters)
	assert.NoError(t, err)
	assert.Equal(t, expectedMergedData["foo"], doc["foo"])
	assert.Equal(t, expectedMergedData["bar"], doc["bar"])
	mockStorage.AssertExpectations(t)
}

func TestEngine_Pull(t *testing.T) {
	mockStorage := new(MockStorageBackend)
	engine := NewEngine(mockStorage, "http://mock-csp")
	ctx := context.Background()

	req := storage.ReplicationPullRequest{
		Collection: "test",
		Checkpoint: 100,
		Limit:      10,
	}

	expectedDocs := []*storage.Document{
		{Id: "test/1", UpdatedAt: 101},
		{Id: "test/2", UpdatedAt: 102},
	}

	mockStorage.On("Query", ctx, mock.MatchedBy(func(q model.Query) bool {
		return q.Collection == "test" && q.Limit == 10 && q.Filters[0].Value == int64(100)
	})).Return(expectedDocs, nil)

	resp, err := engine.Pull(ctx, req)
	assert.NoError(t, err)
	assert.Equal(t, expectedDocs, resp.Documents)
	assert.Equal(t, int64(102), resp.Checkpoint)
	mockStorage.AssertExpectations(t)
}

func TestEngine_Push(t *testing.T) {
	mockStorage := new(MockStorageBackend)
	engine := NewEngine(mockStorage, "http://mock-csp")
	ctx := context.Background()

	doc := &storage.Document{Id: "test/1", Fullpath: "test/1", Collection: "test", Data: map[string]interface{}{"foo": "bar"}, Version: 1}
	req := storage.ReplicationPushRequest{
		Collection: "test",
		Changes: []storage.ReplicationPushChange{
			{Doc: doc},
		},
	}

	// Simulate Get -> Update (Success)
	existingDoc := &storage.Document{Id: "test/1", Version: 1}
	mockStorage.On("Get", ctx, "test/1").Return(existingDoc, nil)
	mockStorage.On("Update", ctx, "test/1", doc.Data, model.Filters{}).Return(nil)

	resp, err := engine.Push(ctx, req)
	assert.NoError(t, err)
	assert.Empty(t, resp.Conflicts)
	mockStorage.AssertExpectations(t)
}

func TestEngine_Push_CreateWhenNotFound(t *testing.T) {
	mockStorage := new(MockStorageBackend)
	engine := NewEngine(mockStorage, "http://mock-csp")
	ctx := context.Background()

	doc := &storage.Document{Id: "test/2", Fullpath: "test/2", Collection: "test", Data: map[string]interface{}{"foo": "bar"}, Version: 0}
	req := storage.ReplicationPushRequest{Collection: "test", Changes: []storage.ReplicationPushChange{{Doc: doc}}}

	mockStorage.On("Get", ctx, "test/2").Return(nil, model.ErrNotFound)
	mockStorage.On("Create", ctx, doc).Return(nil)

	resp, err := engine.Push(ctx, req)
	assert.NoError(t, err)
	assert.Empty(t, resp.Conflicts)
	mockStorage.AssertExpectations(t)
}

func TestEngine_Push_CreateConflictOnInsert(t *testing.T) {
	mockStorage := new(MockStorageBackend)
	engine := NewEngine(mockStorage, "http://mock-csp")
	ctx := context.Background()

	doc := &storage.Document{Id: "test/3", Fullpath: "test/3", Collection: "test", Data: map[string]interface{}{"x": 1}}
	req := storage.ReplicationPushRequest{Collection: "test", Changes: []storage.ReplicationPushChange{{Doc: doc}}}

	mockStorage.On("Get", ctx, "test/3").Return(nil, model.ErrNotFound)
	mockStorage.On("Create", ctx, doc).Return(assert.AnError)

	resp, err := engine.Push(ctx, req)
	assert.NoError(t, err)
	if assert.Len(t, resp.Conflicts, 1) {
		assert.Equal(t, doc, resp.Conflicts[0])
	}
	mockStorage.AssertExpectations(t)
}

func TestEngine_Push_VersionConflictOnUpdate(t *testing.T) {
	mockStorage := new(MockStorageBackend)
	engine := NewEngine(mockStorage, "http://mock-csp")
	ctx := context.Background()

	base := int64(1)
	doc := &storage.Document{Id: "test/4", Fullpath: "test/4", Collection: "test", Data: map[string]interface{}{"n": 2}, Version: 2}
	req := storage.ReplicationPushRequest{Collection: "test", Changes: []storage.ReplicationPushChange{{Doc: doc, BaseVersion: &base}}}

	existing := &storage.Document{Id: "test/4", Version: 2}
	mockStorage.On("Get", ctx, "test/4").Return(existing, nil)

	resp, err := engine.Push(ctx, req)
	assert.NoError(t, err)
	if assert.Len(t, resp.Conflicts, 1) {
		assert.Equal(t, existing, resp.Conflicts[0])
	}
	mockStorage.AssertExpectations(t)
}

func TestEngine_Push_DeleteConflict(t *testing.T) {
	mockStorage := new(MockStorageBackend)
	engine := NewEngine(mockStorage, "http://mock-csp")
	ctx := context.Background()

	base := int64(1)
	doc := &storage.Document{Id: "test/5", Fullpath: "test/5", Collection: "test", Deleted: true}
	req := storage.ReplicationPushRequest{Collection: "test", Changes: []storage.ReplicationPushChange{{Doc: doc, BaseVersion: &base}}}

	existing := &storage.Document{Id: "test/5", Version: 1}
	mockStorage.On("Get", ctx, "test/5").Return(existing, nil)
	mockStorage.On("Delete", ctx, "test/5", model.Filters{{Field: "version", Op: "==", Value: base}}).Return(model.ErrPreconditionFailed)
	mockStorage.On("Get", ctx, "test/5").Return(&storage.Document{Id: "test/5", Version: 2}, nil)

	resp, err := engine.Push(ctx, req)
	assert.NoError(t, err)
	if assert.Len(t, resp.Conflicts, 1) {
		assert.Equal(t, existing, resp.Conflicts[0])
	}
	mockStorage.AssertExpectations(t)
}

func TestEngine_Push_DeleteNotFoundAllowed(t *testing.T) {
	mockStorage := new(MockStorageBackend)
	engine := NewEngine(mockStorage, "http://mock-csp")
	ctx := context.Background()

	doc := &storage.Document{Id: "test/6", Fullpath: "test/6", Collection: "test", Deleted: true}
	req := storage.ReplicationPushRequest{Collection: "test", Changes: []storage.ReplicationPushChange{{Doc: doc}}}

	mockStorage.On("Get", ctx, "test/6").Return(&storage.Document{Id: "test/6", Version: 1}, nil)
	mockStorage.On("Delete", ctx, "test/6", model.Filters{}).Return(model.ErrNotFound)

	resp, err := engine.Push(ctx, req)
	assert.NoError(t, err)
	assert.Empty(t, resp.Conflicts)
	mockStorage.AssertExpectations(t)
}

func TestEngine_Push_UpdateGenericError(t *testing.T) {
	mockStorage := new(MockStorageBackend)
	engine := NewEngine(mockStorage, "http://mock-csp")
	ctx := context.Background()

	doc := &storage.Document{Id: "test/7", Fullpath: "test/7", Collection: "test", Data: map[string]interface{}{"a": 1}}
	req := storage.ReplicationPushRequest{Collection: "test", Changes: []storage.ReplicationPushChange{{Doc: doc}}}

	mockStorage.On("Get", ctx, "test/7").Return(&storage.Document{Id: "test/7", Version: 1}, nil)
	mockStorage.On("Update", ctx, "test/7", doc.Data, model.Filters{}).Return(assert.AnError)

	resp, err := engine.Push(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, resp)
	mockStorage.AssertExpectations(t)
}

func TestEngine_DeleteDocument(t *testing.T) {
	mockStorage := new(MockStorageBackend)
	engine := NewEngine(mockStorage, "http://mock-csp")
	ctx := context.Background()

	path := "test/1"

	t.Run("success", func(t *testing.T) {
		mockStorage.On("Delete", ctx, path, model.Filters(nil)).Return(nil).Once()

		err := engine.DeleteDocument(ctx, path, nil)
		assert.NoError(t, err)
		mockStorage.AssertExpectations(t)
	})

	t.Run("not found", func(t *testing.T) {
		mockStorage.ExpectedCalls = nil
		mockStorage.Calls = nil
		mockStorage.On("Delete", ctx, path, model.Filters(nil)).Return(model.ErrNotFound).Once()

		err := engine.DeleteDocument(ctx, path, nil)
		assert.ErrorIs(t, err, model.ErrNotFound)
		mockStorage.AssertExpectations(t)
	})
}

func TestEngine_ExecuteQuery(t *testing.T) {
	mockStorage := new(MockStorageBackend)
	engine := NewEngine(mockStorage, "http://mock-csp")
	ctx := context.Background()

	q := model.Query{Collection: "c"}
	stored := []*storage.Document{
		{Fullpath: "c/1", Collection: "c", Data: map[string]interface{}{"foo": "bar"}, Version: 2, UpdatedAt: 10, CreatedAt: 5},
	}

	mockStorage.On("Query", ctx, q).Return(stored, nil)

	res, err := engine.ExecuteQuery(ctx, q)
	assert.NoError(t, err)
	assert.Equal(t, model.Document{"foo": "bar", "id": "1", "collection": "c", "version": int64(2), "updatedAt": int64(10), "createdAt": int64(5)}, res[0])
	mockStorage.AssertExpectations(t)
}

func TestEngine_ExecuteQuery_Error(t *testing.T) {
	mockStorage := new(MockStorageBackend)
	engine := NewEngine(mockStorage, "http://mock-csp")
	ctx := context.Background()

	q := model.Query{Collection: "c"}
	mockStorage.On("Query", ctx, q).Return(nil, assert.AnError)

	res, err := engine.ExecuteQuery(ctx, q)
	assert.Error(t, err)
	assert.Nil(t, res)
	mockStorage.AssertExpectations(t)
}

func TestEngine_WatchCollection_StatusError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	engine := &Engine{client: server.Client(), cspURL: server.URL}

	_, err := engine.WatchCollection(context.Background(), "users")
	assert.Error(t, err)
}

func TestEngine_WatchCollection_BadJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("not-json"))
	}))
	defer server.Close()

	engine := &Engine{client: server.Client(), cspURL: server.URL}

	ch, err := engine.WatchCollection(context.Background(), "users")
	assert.NoError(t, err)

	_, ok := <-ch
	assert.False(t, ok)
}

func TestEngine_WatchCollection_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"users/1","type":"create","document":{"fullpath":"users/1","collection":"users","data":{"foo":"bar"}}}`))
	}))
	defer server.Close()

	engine := &Engine{client: server.Client(), cspURL: server.URL}

	ch, err := engine.WatchCollection(context.Background(), "users")
	assert.NoError(t, err)

	evt, ok := <-ch
	assert.True(t, ok)
	assert.Equal(t, "users/1", evt.Id)
	if assert.NotNil(t, evt.Document) {
		assert.Equal(t, "users", evt.Document.Collection)
		assert.Equal(t, "bar", evt.Document.Data["foo"])
	}

	_, ok = <-ch
	assert.False(t, ok)
}
