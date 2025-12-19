package api

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"syntrix/internal/storage"
)

var (
	pathRegex = regexp.MustCompile(`^[a-zA-Z0-9_\-\./]+$`)
	idRegex   = regexp.MustCompile(`^[a-zA-Z0-9_\-\.]{1,64}$`)
)

func validatePathSyntax(path string) error {
	if path == "" {
		return errors.New("path cannot be empty")
	}

	if !pathRegex.MatchString(path) {
		return errors.New("path contains invalid characters")
	}

	// Ensure path does not start or end with /
	if strings.HasPrefix(path, "/") || strings.HasSuffix(path, "/") {
		return errors.New("path cannot start or end with /")
	}

	// Ensure no double slashes
	if strings.Contains(path, "//") {
		return errors.New("path cannot contain empty segments")
	}

	return nil
}

func validateAndExplodeFullpath(path string) (collection string, docId string, err error) {
	if err := validatePathSyntax(path); err != nil {
		return "", "", err
	}

	parts := strings.Split(path, "/")
	if len(parts)%2 != 0 {
		return path, "", nil // It's a collection path
	}

	// It's a document path
	collection = strings.Join(parts[:len(parts)-1], "/")
	docId = parts[len(parts)-1]
	return collection, docId, nil
}

func validateDocumentPath(path string) error {
	if err := validatePathSyntax(path); err != nil {
		return err
	}
	parts := strings.Split(path, "/")
	if len(parts)%2 != 0 {
		return errors.New("invalid document path: must have even number of segments (e.g. collection/doc)")
	}
	return nil
}

func validateCollection(collection string) error {
	if err := validatePathSyntax(collection); err != nil {
		return err
	}
	if len(strings.Split(collection, "/"))%2 != 0 {
		return nil
	}
	return errors.New("invalid collection path: must have odd number of segments (e.g. collection or collection/doc/subcollection)")
}

func validateData(data map[string]interface{}) error {
	if data == nil {
		return errors.New("data cannot be nil")
	}

	if idVal, ok := data["id"]; ok {
		switch idValue := idVal.(type) {
		case string:
			if idValue == "" {
				return errors.New("data field 'id' cannot be empty")
			}

			if !idRegex.MatchString(idVal.(string)) {
				return errors.New("invalid 'id' field: must be 1-64 characters of a-z, A-Z, 0-9, _, ., -")
			}
		case int, int32, int64:
			data["id"] = fmt.Sprintf("%d", idValue)
		default:
			return errors.New("data field 'id' must be a string or integer")
		}
	}
	// TODO: Add more strict validation (e.g. max depth, max size)
	return nil
}

func stripSystemFields(data map[string]interface{}) map[string]interface{} {
	delete(data, "_updated_at")
	return data
}

func validateQuery(q storage.Query) error {
	if err := validateCollection(q.Collection); err != nil {
		return fmt.Errorf("invalid collection: %w", err)
	}
	if q.Limit < 0 {
		return errors.New("limit cannot be negative")
	}
	if q.Limit > 1000 {
		return errors.New("limit cannot exceed 1000")
	}
	for _, f := range q.Filters {
		if f.Field == "" {
			return errors.New("filter field cannot be empty")
		}
		if f.Op == "" {
			return errors.New("filter op cannot be empty")
		}
		switch f.Op {
		case "==", ">", ">=", "<", "<=", "in", "array-contains":
			// Valid
		default:
			return fmt.Errorf("unsupported filter operator: %s", f.Op)
		}
	}
	for _, o := range q.OrderBy {
		if o.Field == "" {
			return errors.New("orderby field cannot be empty")
		}
		if o.Direction != "asc" && o.Direction != "desc" {
			return errors.New("orderby direction must be 'asc' or 'desc'")
		}
	}
	return nil
}

func validateReplicationPull(req storage.ReplicationPullRequest) error {
	if err := validateCollection(req.Collection); err != nil {
		return fmt.Errorf("invalid collection: %w", err)
	}
	if req.Limit < 0 {
		return errors.New("limit cannot be negative")
	}
	if req.Limit > 1000 {
		return errors.New("limit cannot exceed 1000")
	}
	return nil
}

func validateReplicationPush(req storage.ReplicationPushRequest) error {
	if err := validateCollection(req.Collection); err != nil {
		return fmt.Errorf("invalid collection: %w", err)
	}
	for _, change := range req.Changes {
		if change.Doc == nil {
			return errors.New("change document cannot be nil")
		}
		if err := validateDocumentPath(change.Doc.Id); err != nil {
			return fmt.Errorf("invalid document path in change: %w", err)
		}
		// Ensure document path matches collection prefix
		if !strings.HasPrefix(change.Doc.Id, req.Collection+"/") {
			return fmt.Errorf("document path %s does not belong to collection %s", change.Doc.Id, req.Collection)
		}
	}
	return nil
}
