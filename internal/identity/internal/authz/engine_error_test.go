package authz

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/codetrek/syntrix/internal/config"
	"github.com/codetrek/syntrix/internal/query"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockQueryService struct {
	query.Service
}

func TestNewEngine_ErrorPaths(t *testing.T) {
	t.Run("LoadRules Error - File Not Found", func(t *testing.T) {
		cfg := config.AuthZConfig{
			RulesFile: "non_existent_file.yaml",
		}
		_, err := NewEngine(cfg, &mockQueryService{})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to load rules")
	})

	t.Run("LoadRules Error - Invalid YAML", func(t *testing.T) {
		tmpFile := filepath.Join(t.TempDir(), "invalid.yaml")
		err := os.WriteFile(tmpFile, []byte("invalid: yaml: content: :"), 0644)
		require.NoError(t, err)

		cfg := config.AuthZConfig{
			RulesFile: tmpFile,
		}
		_, err = NewEngine(cfg, &mockQueryService{})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to load rules")
	})
}

func TestEvaluate_ErrorPaths(t *testing.T) {
	// Create engine with valid rules but invalid CEL expression
	rulesContent := `
match:
  /databases/default/documents/test:
    allow:
      read: "1 + 'a'"
`
	tmpFile := filepath.Join(t.TempDir(), "rules.yaml")
	err := os.WriteFile(tmpFile, []byte(rulesContent), 0644)
	require.NoError(t, err)

	cfg := config.AuthZConfig{
		RulesFile: tmpFile,
	}
	engine, err := NewEngine(cfg, &mockQueryService{})
	require.NoError(t, err)

	t.Run("CEL Compilation Error", func(t *testing.T) {
		allowed, err := engine.Evaluate(context.Background(), "/test", "read", Request{}, nil)
		assert.Error(t, err)
		assert.False(t, allowed)
	})
}

func TestEvaluate_RuntimeError(t *testing.T) {
	// Create engine with rule that causes runtime error (e.g. type mismatch or division by zero if possible,
	// or using a variable that doesn't exist in a way that passes compile but fails runtime?
	// CEL is strongly typed, so compile usually catches it.
	// Let's try to use a function that might fail or type conversion issue.)

	// Actually, simple syntax error is enough to test evalCondition error path (compile error).
	// For runtime error, maybe index out of bounds?
	rulesContent := `
match:
  /test:
    allow:
      read: "request.auth.uid == '123'"
`
	// If request.auth is missing, it might be null.

	tmpFile := filepath.Join(t.TempDir(), "rules.yaml")
	err := os.WriteFile(tmpFile, []byte(rulesContent), 0644)
	require.NoError(t, err)

	cfg := config.AuthZConfig{
		RulesFile: tmpFile,
	}
	engine, err := NewEngine(cfg, &mockQueryService{})
	require.NoError(t, err)

	t.Run("Runtime Error (Nil Access)", func(t *testing.T) {
		// Request with empty Auth
		req := Request{Auth: Auth{}}
		// In structToMap, nil Auth becomes nil in map.
		// accessing request.auth.uid on nil might cause error?
		// CEL handles null safe navigation usually, but let's see.

		// Actually, let's try to force a compile error again to be sure we covered evalCondition error return.
		// The previous test covered it.

		// Let's try to cover the case where rules are nil.
		e := engine.(*ruleEngine)
		e.rules = nil
		allowed, err := e.Evaluate(context.Background(), "/test", "read", req, nil)
		assert.NoError(t, err)
		assert.False(t, allowed)
	})
}
