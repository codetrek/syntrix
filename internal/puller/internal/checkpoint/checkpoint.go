// Package checkpoint provides resume token persistence for change streams.
package checkpoint

import (
	"context"
	"encoding/base64"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// Store defines the interface for persisting resume tokens.
type Store interface {
	// Save persists the resume token.
	Save(ctx context.Context, token bson.Raw) error

	// Load retrieves the last saved resume token.
	// Returns nil if no checkpoint exists.
	Load(ctx context.Context) (bson.Raw, error)

	// Delete removes the checkpoint.
	Delete(ctx context.Context) error
}

// Policy defines when to save checkpoints.
type Policy struct {
	// Time-based: checkpoint every interval
	Interval time.Duration

	// Event-based: checkpoint every N events
	EventCount int

	// Always checkpoint on graceful shutdown
	OnShutdown bool
}

// DefaultPolicy returns sensible defaults.
func DefaultPolicy() Policy {
	return Policy{
		Interval:   time.Second,
		EventCount: 1000,
		OnShutdown: true,
	}
}

// Tracker tracks when to save checkpoints based on policy.
type Tracker struct {
	policy         Policy
	lastCheckpoint time.Time
	eventsSince    int
	lastToken      bson.Raw
}

// NewTracker creates a new Tracker.
func NewTracker(policy Policy) *Tracker {
	return &Tracker{
		policy:         policy,
		lastCheckpoint: time.Now(),
	}
}

// RecordEvent records an event and returns true if a checkpoint should be saved.
func (t *Tracker) RecordEvent(token bson.Raw) bool {
	t.lastToken = token
	t.eventsSince++

	// Check event count threshold
	if t.eventsSince >= t.policy.EventCount {
		return true
	}

	// Check time interval
	if time.Since(t.lastCheckpoint) >= t.policy.Interval {
		return true
	}

	return false
}

// MarkCheckpointed marks that a checkpoint was saved.
func (t *Tracker) MarkCheckpointed() {
	t.lastCheckpoint = time.Now()
	t.eventsSince = 0
}

// LastToken returns the last recorded token.
func (t *Tracker) LastToken() bson.Raw {
	return t.lastToken
}

// ShouldCheckpointOnShutdown returns true if checkpoint on shutdown is enabled.
func (t *Tracker) ShouldCheckpointOnShutdown() bool {
	return t.policy.OnShutdown && t.lastToken != nil
}

// MongoStore implements Store using MongoDB.
type MongoStore struct {
	collection *mongo.Collection
	backendID  string // Identifies this backend (for multi-backend support)
}

// checkpointDoc is the MongoDB document structure for checkpoints.
type checkpointDoc struct {
	ID        string    `bson:"_id"`
	Token     string    `bson:"token"` // Base64-encoded resume token
	UpdatedAt time.Time `bson:"updated_at"`
}

// NewMongoStore creates a new MongoDB-backed checkpoint store.
func NewMongoStore(db *mongo.Database, backendID string) *MongoStore {
	return &MongoStore{
		collection: db.Collection("_puller_checkpoints"),
		backendID:  backendID,
	}
}

// Save implements Store.
func (s *MongoStore) Save(ctx context.Context, token bson.Raw) error {
	if token == nil {
		return nil
	}

	doc := checkpointDoc{
		ID:        s.backendID,
		Token:     base64.StdEncoding.EncodeToString(token),
		UpdatedAt: time.Now(),
	}

	opts := options.Replace().SetUpsert(true)
	_, err := s.collection.ReplaceOne(ctx, bson.M{"_id": s.backendID}, doc, opts)
	if err != nil {
		return fmt.Errorf("failed to save checkpoint: %w", err)
	}

	return nil
}

// Load implements Store.
func (s *MongoStore) Load(ctx context.Context) (bson.Raw, error) {
	var doc checkpointDoc
	err := s.collection.FindOne(ctx, bson.M{"_id": s.backendID}).Decode(&doc)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, nil // No checkpoint exists
		}
		return nil, fmt.Errorf("failed to load checkpoint: %w", err)
	}

	token, err := base64.StdEncoding.DecodeString(doc.Token)
	if err != nil {
		return nil, fmt.Errorf("failed to decode checkpoint token: %w", err)
	}

	return bson.Raw(token), nil
}

// Delete implements Store.
func (s *MongoStore) Delete(ctx context.Context) error {
	_, err := s.collection.DeleteOne(ctx, bson.M{"_id": s.backendID})
	if err != nil {
		return fmt.Errorf("failed to delete checkpoint: %w", err)
	}
	return nil
}

// EnsureIndexes creates necessary indexes for the checkpoint collection.
func (s *MongoStore) EnsureIndexes(ctx context.Context) error {
	// _id is already indexed by default in MongoDB
	return nil
}
