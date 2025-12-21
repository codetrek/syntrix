package auth

import (
	"context"
	"errors"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

var (
	ErrUserNotFound = errors.New("user not found")
	ErrUserExists   = errors.New("user already exists")
)

type Storage struct {
	db             *mongo.Database
	userColl       *mongo.Collection
	revocationColl *mongo.Collection
}

func NewStorage(db *mongo.Database) *Storage {
	return &Storage{
		db:             db,
		userColl:       db.Collection("auth_users"),
		revocationColl: db.Collection("auth_revocations"),
	}
}

func (s *Storage) CreateUser(ctx context.Context, user *User) error {
	// Ensure username is lowercase
	user.Username = strings.ToLower(user.Username)

	// Check if user exists
	filter := bson.M{"username": user.Username}
	count, err := s.userColl.CountDocuments(ctx, filter)
	if err != nil {
		return err
	}
	if count > 0 {
		return ErrUserExists
	}

	_, err = s.userColl.InsertOne(ctx, user)
	return err
}

func (s *Storage) GetUserByUsername(ctx context.Context, username string) (*User, error) {
	username = strings.ToLower(username)
	filter := bson.M{"username": username}

	var user User
	err := s.userColl.FindOne(ctx, filter).Decode(&user)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, ErrUserNotFound
		}
		return nil, err
	}
	return &user, nil
}

func (s *Storage) GetUserByID(ctx context.Context, id string) (*User, error) {
	filter := bson.M{"_id": id}

	var user User
	err := s.userColl.FindOne(ctx, filter).Decode(&user)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, ErrUserNotFound
		}
		return nil, err
	}
	return &user, nil
}

func (s *Storage) UpdateUserLoginStats(ctx context.Context, id string, lastLogin time.Time, attempts int, lockoutUntil time.Time) error {
	filter := bson.M{"_id": id}
	update := bson.M{
		"$set": bson.M{
			"last_login_at":  lastLogin,
			"login_attempts": attempts,
			"lockout_until":  lockoutUntil,
		},
	}
	_, err := s.userColl.UpdateOne(ctx, filter, update)
	return err
}

// Revocation

type RevokedToken struct {
	JTI       string    `bson:"_id"`
	ExpiresAt time.Time `bson:"expires_at"`
	RevokedAt time.Time `bson:"revoked_at"`
}

func (s *Storage) RevokeToken(ctx context.Context, jti string, expiresAt time.Time) error {
	doc := RevokedToken{
		JTI:       jti,
		ExpiresAt: expiresAt,
		RevokedAt: time.Now(),
	}
	_, err := s.revocationColl.InsertOne(ctx, doc)
	if mongo.IsDuplicateKeyError(err) {
		return nil // Already revoked
	}
	return err
}

func (s *Storage) RevokeTokenImmediate(ctx context.Context, jti string, expiresAt time.Time) error {
	// Set RevokedAt to the past to bypass grace period
	doc := RevokedToken{
		JTI:       jti,
		ExpiresAt: expiresAt,
		RevokedAt: time.Now().Add(-24 * time.Hour),
	}
	_, err := s.revocationColl.InsertOne(ctx, doc)
	if mongo.IsDuplicateKeyError(err) {
		return nil // Already revoked
	}
	return err
}

func (s *Storage) IsRevoked(ctx context.Context, jti string, gracePeriod time.Duration) (bool, error) {
	filter := bson.M{"_id": jti}
	var doc RevokedToken
	err := s.revocationColl.FindOne(ctx, filter).Decode(&doc)
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

// EnsureIndexes creates necessary indexes
func (s *Storage) EnsureIndexes(ctx context.Context) error {
	// User username unique index
	_, err := s.userColl.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "username", Value: 1}},
		Options: options.Index().SetUnique(true),
	})
	if err != nil {
		return err
	}

	// Revocation TTL index
	_, err = s.revocationColl.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "expires_at", Value: 1}},
		Options: options.Index().SetExpireAfterSeconds(0),
	})
	return err
}

func (s *Storage) ListUsers(ctx context.Context, limit int, offset int) ([]*User, error) {
	opts := options.Find().SetLimit(int64(limit)).SetSkip(int64(offset))
	cursor, err := s.userColl.Find(ctx, bson.M{}, opts)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var users []*User
	if err := cursor.All(ctx, &users); err != nil {
		return nil, err
	}
	return users, nil
}

func (s *Storage) UpdateUser(ctx context.Context, user *User) error {
	filter := bson.M{"_id": user.ID}
	update := bson.M{
		"$set": bson.M{
			"roles":      user.Roles,
			"disabled":   user.Disabled,
			"updated_at": time.Now(),
		},
	}
	_, err := s.userColl.UpdateOne(ctx, filter, update)
	return err
}
