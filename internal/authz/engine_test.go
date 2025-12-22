package authz

import (
	"context"
	"os"
	"testing"
	"time"

	"syntrix/internal/common"
	"syntrix/internal/query"
	"syntrix/internal/storage"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockQueryService
type MockQueryService struct {
	mock.Mock
}

func (m *MockQueryService) GetDocument(ctx context.Context, path string) (common.Document, error) {
	args := m.Called(ctx, path)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(common.Document), args.Error(1)
}

func (m *MockQueryService) ListDocuments(ctx context.Context, parent string, pageSize int, pageToken string) ([]*storage.Document, string, error) {
	return nil, "", nil
}

func (m *MockQueryService) QueryDocuments(ctx context.Context, parent string, filter string) ([]*storage.Document, error) {
	return nil, nil
}

func (m *MockQueryService) CreateDocument(ctx context.Context, doc common.Document) error {
	return nil
}

func (m *MockQueryService) ReplaceDocument(ctx context.Context, data common.Document, pred storage.Filters) (common.Document, error) {
	return nil, nil
}

func (m *MockQueryService) PatchDocument(ctx context.Context, data common.Document, pred storage.Filters) (common.Document, error) {
	return nil, nil
}

func (m *MockQueryService) DeleteDocument(ctx context.Context, path string) error {
	return nil
}

func (m *MockQueryService) ExecuteQuery(ctx context.Context, q storage.Query) ([]common.Document, error) {
	return nil, nil
}

func (m *MockQueryService) WatchCollection(ctx context.Context, collection string) (<-chan storage.Event, error) {
	return nil, nil
}

func (m *MockQueryService) Pull(ctx context.Context, req storage.ReplicationPullRequest) (*storage.ReplicationPullResponse, error) {
	return nil, nil
}

func (m *MockQueryService) Push(ctx context.Context, req storage.ReplicationPushRequest) (*storage.ReplicationPushResponse, error) {
	return nil, nil
}

func (m *MockQueryService) RunTransaction(ctx context.Context, fn func(ctx context.Context, tx query.Service) error) error {
	return nil
}

func TestEngine_Evaluate(t *testing.T) {
	// Create a temporary rules file
	rules := `
rules_version: '1'
service: syntrix
match:
  /databases/{database}/documents:
    match:
      /users/{userId}:
        allow:
          read: "request.auth.uid == userId"
          write: "request.auth.uid == userId"
      /public/{doc=**}:
        allow:
          read: "true"
      /rooms/{roomId}:
        allow:
          read: "resource.data.public == true || request.auth.uid in resource.data.members"
          write: "request.auth.uid in resource.data.members"
      /admin/{doc=**}:
        allow:
          read, write: "exists('/databases/' + database + '/documents/admins/' + request.auth.uid)"
`
	tmpFile := t.TempDir() + "/security.yaml"
	err := os.WriteFile(tmpFile, []byte(rules), 0644)
	assert.NoError(t, err)

	mockQuery := new(MockQueryService)
	engine, err := NewEngine(mockQuery)
	assert.NoError(t, err)

	err = engine.LoadRules(tmpFile)
	assert.NoError(t, err)

	// Test Case 1: Allowed Read (User matches)
	req := Request{
		Auth: Auth{UID: "user123"},
		Time: time.Now(),
	}
	allowed, err := engine.Evaluate(context.Background(), "/users/user123", "read", req, nil)
	assert.NoError(t, err)
	assert.True(t, allowed)

	// Test Case 2: Denied Read (User mismatch)
	req = Request{
		Auth: Auth{UID: "otherUser"},
		Time: time.Now(),
	}
	allowed, err = engine.Evaluate(context.Background(), "/users/user123", "read", req, nil)
	assert.NoError(t, err)
	assert.False(t, allowed)

	// Test Case 3: Public Read (Wildcard)
	allowed, err = engine.Evaluate(context.Background(), "/public/some/doc", "read", req, nil)
	assert.NoError(t, err)
	assert.True(t, allowed)

	// Test Case 4: Resource Data Check (Public Room)
	publicRoom := &Resource{
		Data: map[string]interface{}{
			"public": true,
		},
	}
	allowed, err = engine.Evaluate(context.Background(), "/rooms/room1", "read", req, publicRoom)
	assert.NoError(t, err)
	assert.True(t, allowed)

	// Test Case 5: Resource Data Check (Private Room, Member)
	privateRoom := &Resource{
		Data: map[string]interface{}{
			"public":  false,
			"members": []interface{}{"otherUser"},
		},
	}
	allowed, err = engine.Evaluate(context.Background(), "/rooms/room2", "read", req, privateRoom)
	assert.NoError(t, err)
	assert.True(t, allowed)

	// Test Case 6: Resource Data Check (Private Room, Non-Member)
	reqNonMember := Request{
		Auth: Auth{UID: "stranger"},
		Time: time.Now(),
	}
	allowed, err = engine.Evaluate(context.Background(), "/rooms/room2", "read", reqNonMember, privateRoom)
	assert.NoError(t, err)
	assert.False(t, allowed)

	// Test Case 7: Custom Function 'exists' (Admin Check - Success)
	reqAdmin := Request{
		Auth: Auth{UID: "adminUser"},
		Time: time.Now(),
	}
	mockQuery.On("GetDocument", mock.Anything, "admins/adminUser").Return(common.Document{"id": "adminUser"}, nil)

	allowed, err = engine.Evaluate(context.Background(), "/admin/config", "write", reqAdmin, nil)
	assert.NoError(t, err)
	assert.True(t, allowed)

	// Test Case 8: Custom Function 'exists' (Admin Check - Fail)
	mockQuery.On("GetDocument", mock.Anything, "admins/stranger").Return(nil, storage.ErrNotFound)

	allowed, err = engine.Evaluate(context.Background(), "/admin/config", "write", reqNonMember, nil)
	assert.NoError(t, err)
	assert.False(t, allowed)
}

func TestEngine_UpdateRules(t *testing.T) {
	engine, err := NewEngine(new(MockQueryService))
	assert.NoError(t, err)

	rules := `
rules_version: '2'
service: syntrix
match:
  /test:
    allow:
      read: "true"
`
	err = engine.UpdateRules([]byte(rules))
	assert.NoError(t, err)

	loadedRules := engine.GetRules()
	assert.NotNil(t, loadedRules)
	assert.Equal(t, "2", loadedRules.Version)
}
