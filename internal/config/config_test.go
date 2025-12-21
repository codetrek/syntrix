package config

import (
	"os"
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
	defer func() {
		os.Unsetenv("MONGO_URI")
		os.Unsetenv("DB_NAME")
		os.Unsetenv("API_PORT")
	}()

	cfg := LoadConfig()

	assert.Equal(t, "mongodb://test:27017", cfg.Storage.MongoURI)
	assert.Equal(t, "testdb", cfg.Storage.DatabaseName)
	assert.Equal(t, 9090, cfg.API.Port)
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
