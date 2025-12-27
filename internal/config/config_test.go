package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadConfig_Defaults(t *testing.T) {
	// Ensure no env vars interfere
	os.Unsetenv("MONGO_URI")
	os.Unsetenv("DB_NAME")
	os.Unsetenv("GATEWAY_PORT")

	cfg := LoadConfig()

	assert.Equal(t, "mongodb://localhost:27017", cfg.Storage.Backends["default_mongo"].Mongo.URI)
	assert.Equal(t, "syntrix", cfg.Storage.Backends["default_mongo"].Mongo.DatabaseName)
}

func TestLoadConfig_EnvVars(t *testing.T) {
	os.Setenv("MONGO_URI", "mongodb://test:27017")
	os.Setenv("DB_NAME", "testdb")
	os.Setenv("GATEWAY_PORT", "9090")
	os.Setenv("GATEWAY_QUERY_SERVICE_URL", "http://api-env")
	os.Setenv("QUERY_PORT", "9092")
	os.Setenv("QUERY_CSP_SERVICE_URL", "http://csp-env")
	os.Setenv("CSP_PORT", "9093")
	os.Setenv("TRIGGER_NATS_URL", "nats://env:4222")
	os.Setenv("TRIGGER_RULES_FILE", "custom.json")
	defer func() {
		os.Unsetenv("MONGO_URI")
		os.Unsetenv("DB_NAME")
		os.Unsetenv("GATEWAY_PORT")
		os.Unsetenv("GATEWAY_QUERY_SERVICE_URL")
		os.Unsetenv("QUERY_PORT")
		os.Unsetenv("QUERY_CSP_SERVICE_URL")
		os.Unsetenv("CSP_PORT")
		os.Unsetenv("TRIGGER_NATS_URL")
		os.Unsetenv("TRIGGER_RULES_FILE")
	}()

	cfg := LoadConfig()

	assert.Equal(t, "mongodb://test:27017", cfg.Storage.Backends["default_mongo"].Mongo.URI)
	assert.Equal(t, "testdb", cfg.Storage.Backends["default_mongo"].Mongo.DatabaseName)
	assert.Equal(t, 9090, cfg.Gateway.Port)
	assert.Equal(t, "http://api-env", cfg.Gateway.QueryServiceURL)
	assert.Equal(t, 9092, cfg.Query.Port)
	assert.Equal(t, "http://csp-env", cfg.Query.CSPServiceURL)
	assert.Equal(t, 9093, cfg.CSP.Port)
	assert.Equal(t, "nats://env:4222", cfg.Trigger.NatsURL)
	assert.True(t, strings.HasSuffix(cfg.Trigger.RulesFile, filepath.Join("config", "custom.json")))
}

func TestLoadConfig_LoadFileErrors(t *testing.T) {
	require.NoError(t, os.Mkdir("config", 0755))
	defer os.RemoveAll("config")

	// Create a directory where a file is expected to trigger read error path
	require.NoError(t, os.Mkdir("config/config.yml", 0755))

	// Malformed YAML to trigger parse error path
	require.NoError(t, os.WriteFile("config/config.local.yml", []byte("not: [valid"), 0644))

	cfg := LoadConfig()

	// Defaults should remain when files fail to load/parse
	assert.Equal(t, 8080, cfg.Gateway.Port)
	assert.Equal(t, "mongodb://localhost:27017", cfg.Storage.Backends["default_mongo"].Mongo.URI)
}

func TestLoadConfig_File(t *testing.T) {
	err := os.Mkdir("config", 0755)
	require.NoError(t, err)
	defer os.RemoveAll("config")

	// Create a temporary config.yml in the config directory
	configContent := []byte(`
storage:
  backends:
    default_mongo:
      mongo:
        uri: "mongodb://file:27017"
        database_name: "filedb"
gateway:
  port: 7070
`)
	err = os.WriteFile("config/config.yml", configContent, 0644)
	require.NoError(t, err)

	cfg := LoadConfig()

	assert.Equal(t, "mongodb://file:27017", cfg.Storage.Backends["default_mongo"].Mongo.URI)
	assert.Equal(t, "filedb", cfg.Storage.Backends["default_mongo"].Mongo.DatabaseName)
}

func TestResolvePath(t *testing.T) {
	absPath := filepath.Join(t.TempDir(), "absolute", "path", "to", "file")
	assert.Equal(t, absPath, resolvePath("base", absPath))

	relPath := "relative/path/to/file"
	expected := filepath.Join("base", relPath)
	assert.Equal(t, expected, resolvePath("base", relPath))

	assert.Equal(t, "", resolvePath("base", ""))
}

func TestValidate(t *testing.T) {
	// Case 1: Missing default tenant
	cfg := &Config{
		Storage: StorageConfig{
			Tenants: map[string]TenantConfig{},
		},
	}
	err := cfg.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "storage.tenants.default is required")

	// Case 2: Tenant references unknown backend
	cfg = &Config{
		Storage: StorageConfig{
			Tenants: map[string]TenantConfig{
				"default": {Backend: "unknown_backend"},
			},
			Backends: map[string]BackendConfig{
				"existing_backend": {},
			},
		},
	}
	err = cfg.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "references unknown backend")

	// Case 3: Valid config
	cfg = &Config{
		Storage: StorageConfig{
			Tenants: map[string]TenantConfig{
				"default": {Backend: "existing_backend"},
			},
			Backends: map[string]BackendConfig{
				"existing_backend": {},
			},
		},
	}
	err = cfg.Validate()
	assert.NoError(t, err)
}
