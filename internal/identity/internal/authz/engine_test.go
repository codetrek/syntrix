package authz

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/codetrek/syntrix/internal/config"
	"github.com/codetrek/syntrix/internal/storage"
	"github.com/codetrek/syntrix/pkg/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockQueryService
type MockQueryService struct {
	mock.Mock
}

func (m *MockQueryService) GetDocument(ctx context.Context, tenant string, path string) (model.Document, error) {
	args := m.Called(ctx, tenant, path)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(model.Document), args.Error(1)
}

func (m *MockQueryService) ListDocuments(ctx context.Context, parent string, pageSize int, pageToken string) ([]*storage.Document, string, error) {
	return nil, "", nil
}

func (m *MockQueryService) QueryDocuments(ctx context.Context, parent string, filter string) ([]*storage.Document, error) {
	return nil, nil
}

func (m *MockQueryService) CreateDocument(ctx context.Context, tenant string, doc model.Document) error {
	return nil
}

func (m *MockQueryService) ReplaceDocument(ctx context.Context, tenant string, data model.Document, pred model.Filters) (model.Document, error) {
	return nil, nil
}

func (m *MockQueryService) PatchDocument(ctx context.Context, tenant string, data model.Document, pred model.Filters) (model.Document, error) {
	return nil, nil
}

func (m *MockQueryService) DeleteDocument(ctx context.Context, tenant string, path string, pred model.Filters) error {
	return nil
}

func (m *MockQueryService) ExecuteQuery(ctx context.Context, tenant string, q model.Query) ([]model.Document, error) {
	return nil, nil
}

func (m *MockQueryService) WatchCollection(ctx context.Context, tenant string, collection string) (<-chan storage.Event, error) {
	return nil, nil
}

func (m *MockQueryService) Pull(ctx context.Context, tenant string, req storage.ReplicationPullRequest) (*storage.ReplicationPullResponse, error) {
	return nil, nil
}

func (m *MockQueryService) Push(ctx context.Context, tenant string, req storage.ReplicationPushRequest) (*storage.ReplicationPushResponse, error) {
	return nil, nil
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
          read: "request.auth.userId == userId"
          write: "request.auth.userId == userId"
      /public/{doc=**}:
        allow:
          read: "true"
      /rooms/{roomId}:
        allow:
          read: "resource.data.public == true || request.auth.userId in resource.data.members"
          write: "request.auth.userId in resource.data.members"
      /admin/{doc=**}:
        allow:
          read, write: "exists('/databases/' + database + '/documents/admins/' + request.auth.userId)"
`
	tmpFile := t.TempDir() + "/security.yaml"
	err := os.WriteFile(tmpFile, []byte(rules), 0644)
	assert.NoError(t, err)

	mockQuery := new(MockQueryService)
	engine, err := NewEngine(config.AuthZConfig{RulesFile: tmpFile}, mockQuery)
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
	mockQuery.On("GetDocument", mock.Anything, "default", "admins/adminUser").Return(model.Document{"id": "adminUser"}, nil)

	allowed, err = engine.Evaluate(context.Background(), "/admin/config", "write", reqAdmin, nil)
	assert.NoError(t, err)
	assert.True(t, allowed)

	// Test Case 8: Custom Function 'exists' (Admin Check - Fail)
	mockQuery.On("GetDocument", mock.Anything, "default", "admins/stranger").Return(nil, model.ErrNotFound)

	allowed, err = engine.Evaluate(context.Background(), "/admin/config", "write", reqNonMember, nil)
	assert.NoError(t, err)
	assert.False(t, allowed)
}
func TestEvaluate_ActionAliases(t *testing.T) {
	rules := `
rules_version: '1'
service: syntrix
match:
  /databases/{database}/documents:
    match:
      /test/{id}:
        allow:
          read: "true"
          write: "true"
`
	tmpFile := t.TempDir() + "/security.yaml"
	err := os.WriteFile(tmpFile, []byte(rules), 0644)
	assert.NoError(t, err)

	mockQuery := new(MockQueryService)
	engine, err := NewEngine(config.AuthZConfig{RulesFile: tmpFile}, mockQuery)
	assert.NoError(t, err)

	req := Request{Auth: Auth{UID: "user1"}}

	// Test read aliases
	for _, action := range []string{"get", "list"} {
		allowed, err := engine.Evaluate(context.Background(), "/test/1", action, req, nil)
		assert.NoError(t, err)
		assert.True(t, allowed, "Action %s should be allowed by read", action)
	}

	// Test write aliases
	for _, action := range []string{"create", "update", "delete"} {
		allowed, err := engine.Evaluate(context.Background(), "/test/1", action, req, nil)
		assert.NoError(t, err)
		assert.True(t, allowed, "Action %s should be allowed by write", action)
	}

	// Test exact match
	allowed, err := engine.Evaluate(context.Background(), "/test/1", "read", req, nil)
	assert.True(t, allowed)
	allowed, err = engine.Evaluate(context.Background(), "/test/1", "write", req, nil)
	assert.True(t, allowed)

	// Test mismatch
	allowed, err = engine.Evaluate(context.Background(), "/test/1", "other", req, nil)
	assert.False(t, allowed)
}
func TestEngine_UpdateRules(t *testing.T) {
	engine, err := NewEngine(config.AuthZConfig{}, new(MockQueryService))
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

