package trigger

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// LoadTriggersFromFile reads triggers from a JSON or YAML file.
func LoadTriggersFromFile(filename string) ([]*Trigger, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to read trigger rules file: %w", err)
	}

	var triggers []*Trigger
	ext := filepath.Ext(filename)

	switch ext {
	case ".yaml", ".yml":
		if err := yaml.Unmarshal(data, &triggers); err != nil {
			return nil, fmt.Errorf("failed to parse YAML trigger rules: %w", err)
		}
	case ".json":
		if err := json.Unmarshal(data, &triggers); err != nil {
			return nil, fmt.Errorf("failed to parse JSON trigger rules: %w", err)
		}
	default:
		// Try JSON first, then YAML
		if err := json.Unmarshal(data, &triggers); err != nil {
			if err := yaml.Unmarshal(data, &triggers); err != nil {
				return nil, fmt.Errorf("failed to parse trigger rules (unknown format): %w", err)
			}
		}
	}

	return triggers, nil
}
