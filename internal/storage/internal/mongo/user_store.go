package mongo

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/codetrek/syntrix/internal/storage/types"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type userStore struct {
	coll *mongo.Collection
}

func NewUserStore(db *mongo.Database, collectionName string) types.UserStore {
	if collectionName == "" {
		collectionName = "auth_users"
	}
	return &userStore{
		coll: db.Collection(collectionName),
	}
}

func (s *userStore) CreateUser(ctx context.Context, user *types.User) error {
	// Ensure username is lowercase
	user.Username = strings.ToLower(user.Username)

	// Check if user exists
	filter := bson.M{"username": user.Username}
	count, err := s.coll.CountDocuments(ctx, filter)
	if err != nil {
		return err
	}
	if count > 0 {
		return types.ErrUserExists
	}

	_, err = s.coll.InsertOne(ctx, user)
	return err
}

func (s *userStore) GetUserByUsername(ctx context.Context, username string) (*types.User, error) {
	username = strings.ToLower(username)
	filter := bson.M{"username": username}

	var user types.User
	err := s.coll.FindOne(ctx, filter).Decode(&user)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, types.ErrUserNotFound
		}
		return nil, err
	}
	return &user, nil
}

func (s *userStore) GetUserByID(ctx context.Context, id string) (*types.User, error) {
	filter := bson.M{"_id": id}

	var user types.User
	err := s.coll.FindOne(ctx, filter).Decode(&user)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, types.ErrUserNotFound
		}
		return nil, err
	}
	return &user, nil
}

func (s *userStore) UpdateUserLoginStats(ctx context.Context, id string, lastLogin time.Time, attempts int, lockoutUntil time.Time) error {
	filter := bson.M{"_id": id}
	update := bson.M{
		"$set": bson.M{
			"last_login_at":  lastLogin,
			"login_attempts": attempts,
			"lockout_until":  lockoutUntil,
		},
	}
	_, err := s.coll.UpdateOne(ctx, filter, update)
	return err
}

func (s *userStore) ListUsers(ctx context.Context, limit int, offset int) ([]*types.User, error) {
	opts := options.Find().SetLimit(int64(limit)).SetSkip(int64(offset))
	cursor, err := s.coll.Find(ctx, bson.M{}, opts)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var users []*types.User
	if err := cursor.All(ctx, &users); err != nil {
		return nil, err
	}
	return users, nil
}

func (s *userStore) UpdateUser(ctx context.Context, user *types.User) error {
	filter := bson.M{"_id": user.ID}
	update := bson.M{
		"$set": bson.M{
			"roles":      user.Roles,
			"disabled":   user.Disabled,
			"updatedAt":  time.Now(),
		},
	}
	_, err := s.coll.UpdateOne(ctx, filter, update)
	return err
}

func (s *userStore) EnsureIndexes(ctx context.Context) error {
	// User username unique index
	_, err := s.coll.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "username", Value: 1}},
		Options: options.Index().SetUnique(true),
	})
	return err
}

func (s *userStore) Close(ctx context.Context) error {
	return nil
}
