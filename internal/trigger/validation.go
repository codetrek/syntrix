package trigger

import (
	"errors"
	"fmt"
	"regexp"
)

var (
	validNameRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
)

// ValidateTrigger checks if the trigger configuration is valid.
func ValidateTrigger(t *Trigger) error {
	if t.ID == "" {
		return errors.New("trigger id is required")
	}
	if !validNameRegex.MatchString(t.ID) {
		return fmt.Errorf("invalid trigger id: %s", t.ID)
	}

	if t.Tenant == "" {
		return errors.New("tenant is required")
	}
	if !validNameRegex.MatchString(t.Tenant) {
		return fmt.Errorf("invalid tenant: %s", t.Tenant)
	}
	if len(t.Tenant) > 128 {
		return fmt.Errorf("tenant name too long: %s", t.Tenant)
	}

	if t.Collection == "" {
		return errors.New("collection is required")
	}
	// Collection can be a glob, so we don't strictly enforce alphanumeric.
	// But we should check length.
	if len(t.Collection) > 128 {
		return fmt.Errorf("collection name too long: %s", t.Collection)
	}

	if len(t.Events) == 0 {
		return errors.New("at least one event is required")
	}
	for _, evt := range t.Events {
		switch evt {
		case "create", "update", "delete":
		default:
			return fmt.Errorf("invalid event type: %s", evt)
		}
	}

	if t.URL == "" {
		return errors.New("url is required")
	}

	return nil
}
