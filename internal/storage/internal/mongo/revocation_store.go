package mongo

import (
	"context"
	"errors"
	"time"

	"github.com/codetrek/syntrix/internal/storage/types"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type revocationStore struct {
	coll *mongo.Collection
}

func NewRevocationStore(db *mongo.Database, collectionName string) types.TokenRevocationStore {
	if collectionName == "" {
		collectionName = "auth_revocations"
	}
	return &revocationStore{
		coll: db.Collection(collectionName),
	}
}

func (s *revocationStore) RevokeToken(ctx context.Context, jti string, expiresAt time.Time) error {
	doc := types.RevokedToken{
		JTI:       jti,
		ExpiresAt: expiresAt,
		RevokedAt: time.Now(),
	}
	_, err := s.coll.InsertOne(ctx, doc)
	if mongo.IsDuplicateKeyError(err) {
		return nil // Already revoked
	}
	return err
}

func (s *revocationStore) RevokeTokenImmediate(ctx context.Context, jti string, expiresAt time.Time) error {
	// Set RevokedAt to the past to bypass grace period
	doc := types.RevokedToken{
		JTI:       jti,
		ExpiresAt: expiresAt,
		RevokedAt: time.Now().Add(-24 * time.Hour),
	}
	_, err := s.coll.InsertOne(ctx, doc)
	if mongo.IsDuplicateKeyError(err) {
		return nil // Already revoked
	}
	return err
}

func (s *revocationStore) IsRevoked(ctx context.Context, jti string, gracePeriod time.Duration) (bool, error) {
	filter := bson.M{"_id": jti}
	var doc types.RevokedToken
	err := s.coll.FindOne(ctx, filter).Decode(&doc)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return false, nil // Not revoked
		}
		return false, err
	}

	// If grace period is 0, it's revoked immediately
	if gracePeriod == 0 {
		return true, nil
	}

	// Check if within grace period
	if time.Since(doc.RevokedAt) < gracePeriod {
		return false, nil // Treated as not revoked yet (for overlap)
	}

	return true, nil
}

func (s *revocationStore) EnsureIndexes(ctx context.Context) error {
	// Revocation TTL index
	_, err := s.coll.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "expires_at", Value: 1}},
		Options: options.Index().SetExpireAfterSeconds(0),
	})
	return err
}

func (s *revocationStore) Close(ctx context.Context) error {
	return nil
}
