// Package manager provides the index manager that routes events to indexes
// and matches queries to templates.
package manager

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/syntrixbase/syntrix/internal/indexer/encoding"
	"github.com/syntrixbase/syntrix/internal/indexer/store"
	"github.com/syntrixbase/syntrix/internal/indexer/template"
	"github.com/syntrixbase/syntrix/internal/puller/events"
)

// ChangeEvent is an alias for the Puller's StoreChangeEvent.
// This is the event type that the Indexer receives from the Puller subscription.
type ChangeEvent = events.StoreChangeEvent

// Errors
var (
	ErrDatabaseNotFound   = errors.New("database not found")
	ErrNoMatchingIndex    = errors.New("no matching index for query")
	ErrIndexNotFound      = errors.New("index not found")
	ErrIndexRebuilding    = errors.New("index is rebuilding")
	ErrIndexNotReady      = errors.New("index not ready")
	ErrTemplateLoadFailed = errors.New("failed to load templates")
	ErrInvalidPlan        = errors.New("invalid query plan")
)

// FilterOp represents the type of filter operation.
type FilterOp string

const (
	FilterEq  FilterOp = "eq"  // equality
	FilterGt  FilterOp = "gt"  // greater than
	FilterLt  FilterOp = "lt"  // less than
	FilterGte FilterOp = "gte" // greater than or equal
	FilterLte FilterOp = "lte" // less than or equal
)

// Filter represents a query filter on a field.
type Filter struct {
	Field string
	Op    FilterOp
	Value any
}

// OrderField represents an ordering specification.
type OrderField struct {
	Field     string
	Direction encoding.Direction
}

// Plan represents a query plan passed from Query Engine.
type Plan struct {
	Collection  string       // Concrete collection path (e.g., "users/alice/chats")
	Filters     []Filter     // Prefix/range filters
	OrderBy     []OrderField // Ordering specification
	Limit       int          // Max results
	StartAfter  string       // Cursor for pagination (base64-encoded OrderKey)
	ShowDeleted bool         // Include deleted documents in results
}

// DocRef represents a document reference with its OrderKey.
type DocRef struct {
	ID       string // Document ID within collection
	OrderKey []byte // Encoded sort key
}

// Manager manages index operations through the Store interface.
type Manager struct {
	mu          sync.RWMutex
	store       store.Store
	templates   []template.Template        // flat list for backward compatibility
	dbTemplates template.DatabaseTemplates // templates grouped by database
}

// New creates a new index manager with the given store.
func New(s store.Store) *Manager {
	return &Manager{
		store: s,
	}
}

// Store returns the underlying store.
func (m *Manager) Store() store.Store {
	return m.store
}

// LoadTemplatesFromDir loads templates from a directory.
// Templates are grouped by the database field in each file.
func (m *Manager) LoadTemplatesFromDir(dirPath string) error {
	dbTemplates, err := template.LoadFromDir(dirPath)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrTemplateLoadFailed, err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.dbTemplates = dbTemplates
	m.templates = dbTemplates.AllTemplates() // maintain flat list for backward compatibility
	return nil
}

// LoadTemplatesFromBytes loads templates from YAML bytes.
func (m *Manager) LoadTemplatesFromBytes(data []byte) error {
	templates, err := template.LoadFromBytes(data)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrTemplateLoadFailed, err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.templates = templates
	return nil
}

// Templates returns the loaded templates.
func (m *Manager) Templates() []template.Template {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.templates
}

// DatabaseTemplates returns templates grouped by database.
func (m *Manager) DatabaseTemplates() template.DatabaseTemplates {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.dbTemplates
}

// TemplatesForDatabase returns templates for a specific database.
func (m *Manager) TemplatesForDatabase(database string) []template.Template {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.dbTemplates[database]
}

// MatchTemplatesForCollection returns templates that match a collection path.
func (m *Manager) MatchTemplatesForCollection(collection string) []template.MatchResult {
	m.mu.RLock()
	templates := m.templates
	m.mu.RUnlock()
	return template.MatchTemplates(collection, templates)
}

// SelectBestTemplate selects the best template for a query plan.
// Implements Query-to-Index matching rules.
func (m *Manager) SelectBestTemplate(plan Plan) (*template.Template, error) {
	// Get all matching templates for the collection
	matches := m.MatchTemplatesForCollection(plan.Collection)
	if len(matches) == 0 {
		return nil, ErrNoMatchingIndex
	}

	// Filter by query compatibility and include pattern score
	var compatible []matchCandidate
	for _, match := range matches {
		// If showDeleted is requested, the template must have includeDeleted=true
		if plan.ShowDeleted && !match.Template.IncludeDeleted {
			continue
		}
		if queryScore := m.computeQueryScore(plan, match.Template); queryScore > 0 {
			compatible = append(compatible, matchCandidate{
				template:     match.Template,
				queryScore:   queryScore,
				patternScore: match.Score,
			})
		}
	}

	if len(compatible) == 0 {
		return nil, ErrNoMatchingIndex
	}

	// Select best by: pattern specificity first, then query score, then name (tie-breaker)
	best := compatible[0]
	for _, c := range compatible[1:] {
		// More specific pattern wins (higher fixed segments)
		if best.patternScore.Less(c.patternScore) {
			best = c
		} else if c.patternScore.Less(best.patternScore) {
			// best is still better
		} else if c.queryScore > best.queryScore {
			// Same pattern specificity, higher query score wins
			best = c
		} else if c.queryScore == best.queryScore && c.template.Name < best.template.Name {
			// Same scores, lexicographically smaller name wins
			best = c
		}
	}

	return best.template, nil
}

type matchCandidate struct {
	template     *template.Template
	queryScore   int
	patternScore template.PatternScore
}

// computeQueryScore computes how well a template serves a query.
// Returns 0 if the template cannot serve the query.
// Higher score = better match.
func (m *Manager) computeQueryScore(plan Plan, tmpl *template.Template) int {
	// Step 1: Extract equality filters on index prefix
	eqFields := make(map[string]bool)
	for _, f := range tmpl.Fields {
		found := false
		for _, filter := range plan.Filters {
			if filter.Field == f.Field && filter.Op == FilterEq {
				found = true
				break
			}
		}
		if !found {
			break
		}
		eqFields[f.Field] = true
	}

	// Step 2: Check range filter (at most one FIELD with range filters)
	// Multiple bounds on the same field (e.g., >= and <) are allowed
	rangeFields := make(map[string]bool)
	for _, filter := range plan.Filters {
		if eqFields[filter.Field] {
			continue
		}
		if filter.Op == FilterGt || filter.Op == FilterLt || filter.Op == FilterGte || filter.Op == FilterLte {
			rangeFields[filter.Field] = true
		}
	}
	if len(rangeFields) > 1 {
		return 0 // Range filters on multiple fields not supported
	}

	var rangeField string
	for f := range rangeFields {
		rangeField = f
	}

	// Step 3: Determine usable index prefix
	usablePrefixLen := len(eqFields)
	if rangeField != "" {
		// Range filter must be on the next index field
		if usablePrefixLen >= len(tmpl.Fields) {
			return 0
		}
		if tmpl.Fields[usablePrefixLen].Field != rangeField {
			return 0
		}
		usablePrefixLen++
	}

	// Step 4: Check orderBy is prefix of remaining index fields
	// Special case: if orderBy starts with the range field, that's allowed
	// because the index already covers that field's ordering
	orderStart := usablePrefixLen
	for i, orderField := range plan.OrderBy {
		// If this is the first orderBy field and it matches the range field,
		// use the range field's position instead of starting after it
		if i == 0 && rangeField != "" && orderField.Field == rangeField {
			// Check direction matches the template field at range position
			rangeFieldIdx := len(eqFields)
			var expectedDir template.Direction
			if orderField.Direction == encoding.Asc {
				expectedDir = template.Asc
			} else {
				expectedDir = template.Desc
			}
			if tmpl.Fields[rangeFieldIdx].Order != expectedDir {
				return 0 // direction mismatch
			}
			// This orderBy is satisfied by the range field, continue to next
			orderStart = rangeFieldIdx + 1
			continue
		}

		templateIdx := orderStart + i
		// Adjust for the case where we already counted range field in orderBy
		if i > 0 && rangeField != "" && plan.OrderBy[0].Field == rangeField {
			templateIdx = orderStart + i - 1
		}
		if templateIdx >= len(tmpl.Fields) {
			return 0 // orderBy exceeds index
		}
		if tmpl.Fields[templateIdx].Field != orderField.Field {
			return 0 // field mismatch
		}
		// Compare directions (encoding.Direction is int, template.Direction is string)
		var expectedDir template.Direction
		if orderField.Direction == encoding.Asc {
			expectedDir = template.Asc
		} else {
			expectedDir = template.Desc
		}
		if tmpl.Fields[templateIdx].Order != expectedDir {
			return 0 // direction mismatch
		}
	}

	// Score: coverage of filters + orderBy fields
	// Minimum score of 1 for any template that matches the collection pattern
	score := 1 + len(eqFields)*10 + len(plan.OrderBy)*5
	if rangeField != "" {
		score += 3
	}
	// Prefer exact matches
	if orderStart+len(plan.OrderBy) == len(tmpl.Fields) {
		score += 1
	}

	// When there's no orderBy, prefer templates with ascending order on filter fields
	// This simplifies range filter bounds handling and is more predictable
	if len(plan.OrderBy) == 0 {
		hasDescField := false
		for i := 0; i < usablePrefixLen && i < len(tmpl.Fields); i++ {
			if tmpl.Fields[i].Order == template.Desc {
				hasDescField = true
				break
			}
		}
		if !hasDescField {
			score += 2 // Prefer ascending when no orderBy specified
		}
	}

	return score
}

// Search executes a search using the Store interface.
func (m *Manager) Search(ctx context.Context, database string, plan Plan) ([]DocRef, error) {
	if err := m.validatePlan(plan); err != nil {
		return nil, err
	}

	// Select best template
	tmpl, err := m.SelectBestTemplate(plan)
	if err != nil {
		return nil, err
	}

	pattern := tmpl.NormalizedPattern()
	tmplID := tmpl.Identity()

	// Build search options
	opts, err := m.buildSearchOptions(plan, tmpl)
	if err != nil {
		return nil, err
	}

	// Execute search via Store interface
	results, err := m.store.Search(database, pattern, tmplID, opts)
	if err != nil {
		return nil, err
	}

	// Convert store.DocRef to manager.DocRef
	docRefs := make([]DocRef, len(results))
	for i, r := range results {
		docRefs[i] = DocRef{ID: r.ID, OrderKey: r.OrderKey}
	}

	return docRefs, nil
}

func (m *Manager) validatePlan(plan Plan) error {
	if plan.Collection == "" {
		return fmt.Errorf("%w: collection is required", ErrInvalidPlan)
	}
	return nil
}

func (m *Manager) buildSearchOptions(plan Plan, tmpl *template.Template) (store.SearchOptions, error) {
	opts := store.SearchOptions{
		Limit: plan.Limit,
	}
	if opts.Limit <= 0 {
		opts.Limit = 100 // default
	}

	// Handle startAfter cursor
	if plan.StartAfter != "" {
		key, err := encoding.DecodeBase64(plan.StartAfter)
		if err != nil {
			return opts, fmt.Errorf("invalid cursor: %w", err)
		}
		opts.StartAfter = key
	}

	// Build lower/upper bounds from filters
	// We need to encode equality prefix + optional range bounds
	lowerFields := make([]encoding.Field, 0, len(tmpl.Fields))
	upperFields := make([]encoding.Field, 0, len(tmpl.Fields))

	// Track inclusivity for range bounds (only applies to the last field)
	var rangeLowerInclusive, rangeUpperInclusive bool = true, true
	var hasRangeLower, hasRangeUpper bool

	// Track which fields have filters
	filterMap := make(map[string][]Filter)
	for _, f := range plan.Filters {
		filterMap[f.Field] = append(filterMap[f.Field], f)
	}

	// Process template fields in order
	for _, tf := range tmpl.Fields {
		filters := filterMap[tf.Field]
		if len(filters) == 0 {
			// No filter on this field - stop building bounds here
			// All subsequent fields are unconstrained
			break
		}

		var dir encoding.Direction
		if tf.Order == template.Desc {
			dir = encoding.Desc
		} else {
			dir = encoding.Asc
		}

		// Check for equality filter first
		var eqFilter *Filter
		var rangeFilters []Filter
		for j := range filters {
			if filters[j].Op == FilterEq {
				eqFilter = &filters[j]
			} else {
				rangeFilters = append(rangeFilters, filters[j])
			}
		}

		if eqFilter != nil {
			// Equality: add to both lower and upper
			lowerFields = append(lowerFields, encoding.Field{Value: eqFilter.Value, Direction: dir})
			upperFields = append(upperFields, encoding.Field{Value: eqFilter.Value, Direction: dir})
			continue
		}

		// Range filter - this should be on the field after the equality prefix
		// Process gt/gte for lower bound, lt/lte for upper bound
		// NOTE: For descending order fields, the bounds are INVERTED because
		// smaller values encode to larger keys.
		var hasLowerBound, hasUpperBound bool
		var lowerVal, upperVal any
		var lowerInclusive, upperInclusive bool

		for _, rf := range rangeFilters {
			switch rf.Op {
			case FilterGt:
				if dir == encoding.Asc {
					lowerVal = rf.Value
					lowerInclusive = false
					hasLowerBound = true
				} else {
					// Descending: > becomes upper bound (larger keys)
					upperVal = rf.Value
					upperInclusive = false
					hasUpperBound = true
				}
			case FilterGte:
				if dir == encoding.Asc {
					lowerVal = rf.Value
					lowerInclusive = true
					hasLowerBound = true
				} else {
					upperVal = rf.Value
					upperInclusive = true
					hasUpperBound = true
				}
			case FilterLt:
				if dir == encoding.Asc {
					upperVal = rf.Value
					upperInclusive = false
					hasUpperBound = true
				} else {
					// Descending: < becomes lower bound (larger keys)
					lowerVal = rf.Value
					lowerInclusive = false
					hasLowerBound = true
				}
			case FilterLte:
				if dir == encoding.Asc {
					upperVal = rf.Value
					upperInclusive = true
					hasUpperBound = true
				} else {
					lowerVal = rf.Value
					lowerInclusive = true
					hasLowerBound = true
				}
			}
		}

		// For range filters, we add them to the bounds
		// After a range filter, we cannot add more constraints
		if hasLowerBound {
			lowerFields = append(lowerFields, encoding.Field{Value: lowerVal, Direction: dir})
			rangeLowerInclusive = lowerInclusive
			hasRangeLower = true
		}
		if hasUpperBound {
			upperFields = append(upperFields, encoding.Field{Value: upperVal, Direction: dir})
			rangeUpperInclusive = upperInclusive
			hasRangeUpper = true
		}

		// After a range filter, break - can't have more constraints
		if hasLowerBound || hasUpperBound {
			break
		}
	}

	// Encode bounds if we have any fields
	// Use EncodePrefix to avoid adding id_len suffix, which would break prefix matching
	if len(lowerFields) > 0 {
		lower, err := encoding.EncodePrefix(lowerFields)
		if err != nil {
			return opts, fmt.Errorf("failed to encode lower bound: %w", err)
		}
		// Index search uses Lower as inclusive (AscendGreaterOrEqual)
		// For exclusive lower bound (> not >=), we need to skip past all keys with this value
		// by appending max suffix to the encoded value
		if hasRangeLower && !rangeLowerInclusive {
			opts.Lower = appendMaxSuffix(lower)
		} else {
			opts.Lower = lower
		}
	}

	if len(upperFields) > 0 {
		upper, err := encoding.EncodePrefix(upperFields)
		if err != nil {
			return opts, fmt.Errorf("failed to encode upper bound: %w", err)
		}
		// Index search uses Upper as exclusive (bytes.Compare >= 0 stops)
		// - For equality filters (no range): need max suffix to include all with prefix
		// - For inclusive upper bound (<= not <): need max suffix to include this value
		// - For exclusive upper bound (< not <=): use value directly
		if !hasRangeUpper {
			// Pure equality filters - need to include all documents with this prefix
			opts.Upper = appendMaxSuffix(upper)
		} else if rangeUpperInclusive {
			// Inclusive range (<=) - need to include documents with exactly this value
			opts.Upper = appendMaxSuffix(upper)
		} else {
			// Exclusive range (<) - stop before reaching this value
			opts.Upper = upper
		}
	}

	return opts, nil
}

// appendMaxSuffix appends 0xFF bytes to create an upper bound that includes all keys with the given prefix.
func appendMaxSuffix(b []byte) []byte {
	if len(b) == 0 {
		return nil
	}
	result := make([]byte, len(b)+64)
	copy(result, b)
	for i := len(b); i < len(result); i++ {
		result[i] = 0xFF
	}
	return result
}

// Upsert inserts or updates a document in the index.
func (m *Manager) Upsert(database, pattern, tmplID, docID string, orderKey []byte, progress string) error {
	return m.store.Upsert(database, pattern, tmplID, docID, orderKey, progress)
}

// Delete removes a document from the index.
func (m *Manager) Delete(database, pattern, tmplID, docID string, progress string) error {
	return m.store.Delete(database, pattern, tmplID, docID, progress)
}

// DeleteIndex removes all data for an index.
func (m *Manager) DeleteIndex(database, pattern, tmplID string) error {
	return m.store.DeleteIndex(database, pattern, tmplID)
}

// SetState sets the state of an index.
func (m *Manager) SetState(database, pattern, tmplID string, state store.IndexState) error {
	return m.store.SetState(database, pattern, tmplID, state)
}

// GetState returns the state of an index.
func (m *Manager) GetState(database, pattern, tmplID string) (store.IndexState, error) {
	return m.store.GetState(database, pattern, tmplID)
}

// LoadProgress loads the event processing progress.
func (m *Manager) LoadProgress() (string, error) {
	return m.store.LoadProgress()
}

// Flush flushes pending writes to storage.
func (m *Manager) Flush() error {
	return m.store.Flush()
}

// Close closes the store.
func (m *Manager) Close() error {
	return m.store.Close()
}

// Stats contains aggregate readings for an Indexer service instance.
type Stats struct {
	// TemplateCount counts loaded templates, not instantiated indexes.
	TemplateCount int
	// EventsApplied counts events reaching the end of matching-template processing,
	// including events with logged template write failures.
	EventsApplied int64
	// LastEventTime is local processing time in Unix seconds; zero means unrecorded.
	LastEventTime int64
}

// HealthStatus represents the health status.
type HealthStatus string

const (
	HealthOK        HealthStatus = "ok"
	HealthDegraded  HealthStatus = "degraded"
	HealthUnhealthy HealthStatus = "unhealthy"
)

// IndexHealth represents the health of a single index.
type IndexHealth struct {
	State    string
	DocCount int64
}

// Health represents the health status of the indexer.
type Health struct {
	Status    HealthStatus           // Overall status
	Indexes   map[string]IndexHealth // Per-index status (key: database|pattern|templateID)
	LastError string                 // Last error message if any
}

// Stats returns current statistics.
func (m *Manager) Stats() Stats {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return Stats{
		TemplateCount: len(m.templates),
	}
}
