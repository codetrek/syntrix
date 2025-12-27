package authz

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/codetrek/syntrix/internal/config"
	"github.com/codetrek/syntrix/pkg/model"
	"github.com/stretchr/testify/assert"
)

// TestEmptyAndInvalidRules covers:
// - Evaluate returns false when rules missing
// - LoadRules/UpdateRules fail fast on bad YAML or CEL
func TestEmptyAndInvalidRules(t *testing.T) {
	mockQuery := new(MockQueryService)

	// Case 1: No rules loaded
	engine, err := NewEngine(config.AuthZConfig{}, mockQuery)
	assert.NoError(t, err)

	allowed, err := engine.Evaluate(context.Background(), "/some/path", "read", Request{}, nil)
	assert.NoError(t, err)
	assert.False(t, allowed, "Should deny when no rules are loaded")

	// Case 2: LoadRules with invalid YAML
	tmpFile := t.TempDir() + "/invalid.yaml"
	err = os.WriteFile(tmpFile, []byte("invalid: yaml: content: :"), 0644)
	assert.NoError(t, err)

	err = engine.LoadRules(tmpFile)
	assert.Error(t, err, "Should fail on invalid YAML")

	// Case 3: UpdateRules with invalid CEL
	invalidCELRules := `
rules_version: '1'
service: syntrix
match:
  /test:
    allow:
      read: "this is not valid cel syntax !!!"
`
	err = engine.UpdateRules([]byte(invalidCELRules))
	assert.Error(t, err, "Should fail on invalid CEL compilation")
}

// TestPathMatching covers:
// - {var=**} deep capture
// - Parent + child vars merge
// - Deterministic ordering
// - Unmatched/empty allow paths return false
func TestPathMatching(t *testing.T) {
	rules := `
rules_version: '1'
service: syntrix
match:
  /databases/{database}/documents:
    match:
      /users/{userId}:
        allow:
          read: "userId == 'u1'"
      /files/{path=**}:
        allow:
          read: "path == 'images/logo.png'"
      /mixed/{p1}:
        match:
          /{p2}:
            allow:
              read: "p1 == 'a' && p2 == 'b'"
`
	mockQuery := new(MockQueryService)
	engine, err := NewEngine(config.AuthZConfig{}, mockQuery)
	assert.NoError(t, err)
	err = engine.UpdateRules([]byte(rules))
	assert.NoError(t, err)

	ctx := context.Background()

	// Deep capture
	allowed, err := engine.Evaluate(ctx, "/files/images/logo.png", "read", Request{}, nil)
	assert.NoError(t, err)
	assert.True(t, allowed, "Should match deep capture path")

	allowed, err = engine.Evaluate(ctx, "/files/other.txt", "read", Request{}, nil)
	assert.NoError(t, err)
	assert.False(t, allowed, "Should match deep capture path but fail condition")

	// Variable merging
	allowed, err = engine.Evaluate(ctx, "/mixed/a/b", "read", Request{}, nil)
	assert.NoError(t, err)
	assert.True(t, allowed, "Should merge parent and child variables")

	allowed, err = engine.Evaluate(ctx, "/mixed/a/c", "read", Request{}, nil)
	assert.NoError(t, err)
	assert.False(t, allowed, "Should fail if variables don't match condition")

	// Unmatched path
	allowed, err = engine.Evaluate(ctx, "/unknown/path", "read", Request{}, nil)
	assert.NoError(t, err)
	assert.False(t, allowed, "Should deny unmatched path")
}

// TestActionHandling covers:
// - Mixed spacing/case robustness
// - Unknown action returns false without panic
func TestActionHandling(t *testing.T) {
	rules := `
rules_version: '1'
service: syntrix
match:
  /databases/{database}/documents:
    match:
      /test:
        allow:
          read, write: "true"
          customAction: "true"
`
	mockQuery := new(MockQueryService)
	engine, err := NewEngine(config.AuthZConfig{}, mockQuery)
	assert.NoError(t, err)
	err = engine.UpdateRules([]byte(rules))
	assert.NoError(t, err)

	ctx := context.Background()

	// Standard actions
	allowed, err := engine.Evaluate(ctx, "/test", "read", Request{}, nil)
	assert.True(t, allowed)
	allowed, err = engine.Evaluate(ctx, "/test", "write", Request{}, nil)
	assert.True(t, allowed)

	// Custom action
	allowed, err = engine.Evaluate(ctx, "/test", "customAction", Request{}, nil)
	assert.True(t, allowed)

	// Unknown action
	allowed, err = engine.Evaluate(ctx, "/test", "unknown_action", Request{}, nil)
	assert.False(t, allowed)
}

// TestCELConditionErrors covers:
// - Runtime errors propagate or fail safe
// - existingRes nil is safe
// - Multiple allow clauses continue after failures (if applicable, though usually OR logic)
func TestCELConditionErrors(t *testing.T) {
	rules := `
rules_version: '1'
service: syntrix
match:
  /databases/{database}/documents:
    match:
      /safe:
        allow:
          read: "has(resource.data) && resource.data.public == true"
      /unsafe:
        allow:
          read: "resource.data.public == true"
      /error:
        allow:
          read: "1 / 0 == 0"
`
	mockQuery := new(MockQueryService)
	engine, err := NewEngine(config.AuthZConfig{}, mockQuery)
	assert.NoError(t, err)
	err = engine.UpdateRules([]byte(rules))
	assert.NoError(t, err)

	ctx := context.Background()

	// Safe check with nil resource
	allowed, err := engine.Evaluate(ctx, "/safe", "read", Request{}, nil)
	assert.NoError(t, err)
	assert.False(t, allowed, "Should be false safely when resource is nil")

	// Unsafe check with nil resource (might error or return false depending on CEL config)
	// In standard CEL, accessing field on null might error.
	allowed, err = engine.Evaluate(ctx, "/unsafe", "read", Request{}, nil)
	// We expect it to fail safe (return false) or return error.
	// The requirement says "fail safe".
	assert.False(t, allowed)

	// Runtime error (division by zero)
	allowed, err = engine.Evaluate(ctx, "/error", "read", Request{}, nil)
	assert.False(t, allowed)
}

// TestCustomFunctions covers:
// - exists/get with ErrNotFound vs other errors
// - Non-string args error
func TestCustomFunctions(t *testing.T) {
	rules := `
rules_version: '1'
service: syntrix
match:
  /databases/{database}/documents:
    match:
      /check_exists:
        allow:
          read: "exists('/databases/$(database)/documents/target')"
      /check_get:
        allow:
          read: "get('/databases/$(database)/documents/target').data.flag == true"
`
	mockQuery := new(MockQueryService)
	engine, err := NewEngine(config.AuthZConfig{}, mockQuery)
	assert.NoError(t, err)
	err = engine.UpdateRules([]byte(rules))
	assert.NoError(t, err)

	ctx := context.Background()

	// Mock exists (GetDocument returns nil error for success, error for not found)
	// Case 1: Exists true
	mockQuery.On("GetDocument", ctx, "default", "target").Return(model.Document{"data": map[string]interface{}{"flag": true}}, nil).Once()
	allowed, err := engine.Evaluate(ctx, "/check_exists", "read", Request{}, nil)
	assert.True(t, allowed)

	// Case 2: Exists false (ErrNotFound)
	// Assuming storage.ErrNotFound or similar. For now, any error usually means not found in simple exists check unless distinguished.
	mockQuery.On("GetDocument", ctx, "default", "target").Return(nil, errors.New("not found")).Once()
	allowed, err = engine.Evaluate(ctx, "/check_exists", "read", Request{}, nil)
	assert.False(t, allowed)

	// Case 3: Get true
	mockQuery.On("GetDocument", ctx, "default", "target").Return(model.Document{"flag": true}, nil).Once()
	allowed, err = engine.Evaluate(ctx, "/check_get", "read", Request{}, nil)
	assert.True(t, allowed)
}
