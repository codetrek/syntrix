package model

// Query represents a database query
type Query struct {
	Collection  string  `json:"collection"`
	Filters     Filters `json:"filters"`
	OrderBy     []Order `json:"orderBy"`
	Limit       int     `json:"limit"`
	StartAfter  string  `json:"startAfter"` // Cursor (usually the last document ID or sort key)
	ShowDeleted bool    `json:"showDeleted"`
}
