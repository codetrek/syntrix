package trigger

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadTriggersFromFile(t *testing.T) {
	// 1. Create Temp JSON File
	jsonContent := `[
		{
			"triggerId": "t1",
			"collection": "users",
			"events": ["create"],
			"condition": "true",
			"url": "http://example.com"
		}
	]`
	jsonFile, err := os.CreateTemp("", "triggers-*.json")
	require.NoError(t, err)
	defer os.Remove(jsonFile.Name())
	_, err = jsonFile.WriteString(jsonContent)
	require.NoError(t, err)
	jsonFile.Close()

	// 2. Test JSON Load
	triggers, err := LoadTriggersFromFile(jsonFile.Name())
	require.NoError(t, err)
	require.Len(t, triggers, 1)
	assert.Equal(t, "t1", triggers[0].ID)
	assert.Equal(t, "users", triggers[0].Collection)

	// 3. Create Temp YAML File
	yamlContent := `
- triggerId: t2
  collection: orders
  events:
    - update
  condition: "price > 100"
  url: "http://example.org"
`
	yamlFile, err := os.CreateTemp("", "triggers-*.yaml")
	require.NoError(t, err)
	defer os.Remove(yamlFile.Name())
	_, err = yamlFile.WriteString(yamlContent)
	require.NoError(t, err)
	yamlFile.Close()

	// 4. Test YAML Load
	triggers, err = LoadTriggersFromFile(yamlFile.Name())
	require.NoError(t, err)
	require.Len(t, triggers, 1)
	assert.Equal(t, "t2", triggers[0].ID)
	assert.Equal(t, "orders", triggers[0].Collection)
}

func TestLoadTriggersFromFile_NotFound(t *testing.T) {
	_, err := LoadTriggersFromFile("non-existent-file.json")
	assert.Error(t, err)
}

func TestLoadTriggersFromFile_InvalidFormat(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "invalid-*.txt")
	require.NoError(t, err)
	defer os.Remove(tmpFile.Name())
	_, err = tmpFile.WriteString("invalid content")
	require.NoError(t, err)
	tmpFile.Close()

	_, err = LoadTriggersFromFile(tmpFile.Name())
	assert.Error(t, err)
}
