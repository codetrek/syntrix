package mongo

import (
	"context"

	"github.com/codetrek/syntrix/internal/storage/types"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type authProvider struct {
	client      *mongo.Client
	users       types.UserStore
	revocations types.TokenRevocationStore
}

// NewAuthProvider initializes a new MongoDB auth provider
func NewAuthProvider(ctx context.Context, uri string, dbName string) (types.AuthProvider, error) {
	clientOpts := options.Client().ApplyURI(uri)
	client, err := mongo.Connect(ctx, clientOpts)
	if err != nil {
		return nil, err
	}

	// Ping the database to verify connection
	if err := client.Ping(ctx, nil); err != nil {
		return nil, err
	}

	db := client.Database(dbName)
	
	users := NewUserStore(db)
	revocations := NewRevocationStore(db)
	
	if err := users.EnsureIndexes(ctx); err != nil {
		client.Disconnect(ctx)
		return nil, err
	}
	
	if err := revocations.EnsureIndexes(ctx); err != nil {
		client.Disconnect(ctx)
		return nil, err
	}
	
	return &authProvider{
		client:      client,
		users:       users,
		revocations: revocations,
	}, nil
}

func (p *authProvider) Users() types.UserStore {
	return p.users
}

func (p *authProvider) Revocations() types.TokenRevocationStore {
	return p.revocations
}

func (p *authProvider) Close(ctx context.Context) error {
	return p.client.Disconnect(ctx)
}
