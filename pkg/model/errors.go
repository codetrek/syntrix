package model

import "errors"

var (
	// ErrNotFound is returned when a document is not found
	ErrNotFound = errors.New("document not found")
	// ErrExists is returned when trying to create a document that already exists
	ErrExists = errors.New("document already exists")
	// ErrPreconditionFailed is returned when a CAS operation fails due to unmet preconditions
	ErrPreconditionFailed = errors.New("precondition failed")
	// ErrUnauthorized is returned when authentication fails
	ErrPermissionDenied = errors.New("permission denied")
	// ErrInvalidQuery is returned when a query is malformed
	ErrInvalidQuery = errors.New("invalid query")
)
