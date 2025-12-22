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
	os.Unsetenv("API_PORT")

	cfg := LoadConfig()

	assert.Equal(t, "mongodb://localhost:27017", cfg.Storage.MongoURI)
	assert.Equal(t, "syntrix", cfg.Storage.DatabaseName)
	assert.Equal(t, 8080, cfg.API.Port)
}

func TestLoadConfig_EnvVars(t *testing.T) {
	os.Setenv("MONGO_URI", "mongodb://test:27017")
	os.Setenv("DB_NAME", "testdb")
	os.Setenv("API_PORT", "9090")
	os.Setenv("API_QUERY_SERVICE_URL", "http://api-env")
	os.Setenv("REALTIME_PORT", "9091")
	os.Setenv("REALTIME_QUERY_SERVICE_URL", "http://rt-env")
	os.Setenv("QUERY_PORT", "9092")
	os.Setenv("QUERY_CSP_SERVICE_URL", "http://csp-env")
	os.Setenv("CSP_PORT", "9093")
	os.Setenv("TRIGGER_NATS_URL", "nats://env:4222")
	os.Setenv("TRIGGER_RULES_FILE", "custom.json")
	defer func() {
		os.Unsetenv("MONGO_URI")
		os.Unsetenv("DB_NAME")
		os.Unsetenv("API_PORT")
		os.Unsetenv("API_QUERY_SERVICE_URL")
		os.Unsetenv("REALTIME_PORT")
		os.Unsetenv("REALTIME_QUERY_SERVICE_URL")
		os.Unsetenv("QUERY_PORT")
		os.Unsetenv("QUERY_CSP_SERVICE_URL")
		os.Unsetenv("CSP_PORT")
		os.Unsetenv("TRIGGER_NATS_URL")
		os.Unsetenv("TRIGGER_RULES_FILE")
	}()

	cfg := LoadConfig()

	assert.Equal(t, "mongodb://test:27017", cfg.Storage.MongoURI)
	assert.Equal(t, "testdb", cfg.Storage.DatabaseName)
	assert.Equal(t, 9090, cfg.API.Port)
	assert.Equal(t, "http://api-env", cfg.API.QueryServiceURL)
	assert.Equal(t, 9091, cfg.Realtime.Port)
	assert.Equal(t, "http://rt-env", cfg.Realtime.QueryServiceURL)
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
	assert.Equal(t, 8080, cfg.API.Port)
	assert.Equal(t, "mongodb://localhost:27017", cfg.Storage.MongoURI)
}

func TestLoadConfig_FileOverride(t *testing.T) {
	// Create config directory
	err := os.Mkdir("config", 0755)
	require.NoError(t, err)
	defer os.RemoveAll("config")

	// Create a temporary config.yml in the config directory
	configContent := []byte(`
storage:
  mongo_uri: "mongodb://file:27017"
  database_name: "filedb"
api:
  port: 7070
`)
	err = os.WriteFile("config/config.yml", configContent, 0644)
	require.NoError(t, err)

	cfg := LoadConfig()

	assert.Equal(t, "mongodb://file:27017", cfg.Storage.MongoURI)
	assert.Equal(t, "filedb", cfg.Storage.DatabaseName)
	assert.Equal(t, 7070, cfg.API.Port)
}

func TestLoadConfig_LocalFileOverride(t *testing.T) {
	// Create config directory
	err := os.Mkdir("config", 0755)
	require.NoError(t, err)
	defer os.RemoveAll("config")

	// Create config.yml
	err = os.WriteFile("config/config.yml", []byte(`
storage:
  mongo_uri: "mongodb://file:27017"
  database_name: "filedb"
api:
  port: 7070
`), 0644)
	require.NoError(t, err)

	// Create config.local.yml
	err = os.WriteFile("config/config.local.yml", []byte(`
storage:
  mongo_uri: "mongodb://local:27017"
`), 0644)
	require.NoError(t, err)

	cfg := LoadConfig()

	assert.Equal(t, "mongodb://local:27017", cfg.Storage.MongoURI) // Overridden
	assert.Equal(t, "filedb", cfg.Storage.DatabaseName)            // Inherited from config.yml
	assert.Equal(t, 7070, cfg.API.Port)                            // Inherited from config.yml
}

func TestLoadConfig_EnvOverrideFile(t *testing.T) {
	// Create config directory
	err := os.Mkdir("config", 0755)
	require.NoError(t, err)
	defer os.RemoveAll("config")

	// Create config.yml
	err = os.WriteFile("config/config.yml", []byte(`
storage:
  mongo_uri: "mongodb://file:27017"
`), 0644)
	require.NoError(t, err)

	os.Setenv("MONGO_URI", "mongodb://env:27017")
	defer os.Unsetenv("MONGO_URI")

	cfg := LoadConfig()

	assert.Equal(t, "mongodb://env:27017", cfg.Storage.MongoURI)
}
