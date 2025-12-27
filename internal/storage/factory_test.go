package storage

import (
	"context"
	"testing"
	"time"

	"github.com/codetrek/syntrix/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type mockMongoProvider struct {
	client *mongo.Client
	dbName string
}

func (m *mockMongoProvider) Client() *mongo.Client {
	return m.client
}

func (m *mockMongoProvider) DatabaseName() string {
	return m.dbName
}

func (m *mockMongoProvider) Close(ctx context.Context) error {
	return nil
}

// Mock provider creation
var originalNewMongoProvider = newMongoProvider

func setupMockProvider() {
	newMongoProvider = func(ctx context.Context, uri, dbName string) (Provider, error) {
		// Return a dummy client (won't connect but satisfies interface)
		client, _ := mongo.Connect(ctx, options.Client().ApplyURI("mongodb://mock"))
		return &mockMongoProvider{client: client, dbName: dbName}, nil
	}
}

func teardownMockProvider() {
	newMongoProvider = originalNewMongoProvider
}

const (
	testMongoURI = "mongodb://localhost:27017"
	testDBName   = "syntrix_test_factory"
)

func TestNewFactory(t *testing.T) {
	setupMockProvider()
	defer teardownMockProvider()

	cfg := &config.Config{
		Storage: config.StorageConfig{
			Backends: map[string]config.BackendConfig{
				"primary": {
					Type: "mongo",
					Mongo: config.MongoConfig{
						URI:          testMongoURI,
						DatabaseName: testDBName,
					},
				},
			},
			Topology: config.TopologyConfig{
				Document: config.DocumentTopology{
					BaseTopology: config.BaseTopology{
						Strategy: "single",
						Primary:  "primary",
					},
					DataCollection: "docs",
					SysCollection:  "sys",
				},
				User: config.CollectionTopology{
					BaseTopology: config.BaseTopology{
						Strategy: "single",
						Primary:  "primary",
					},
					Collection: "users",
				},
				Revocation: config.CollectionTopology{
					BaseTopology: config.BaseTopology{
						Strategy: "single",
						Primary:  "primary",
					},
					Collection: "revocations",
				},
			},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	f, err := NewFactory(ctx, cfg)
	require.NoError(t, err)
	defer f.Close()

	assert.NotNil(t, f.Document())
	assert.NotNil(t, f.User())
	assert.NotNil(t, f.Revocation())
}

func TestNewFactory_TenantConfig(t *testing.T) {
	setupMockProvider()
	defer teardownMockProvider()

	cfg := &config.Config{
		Storage: config.StorageConfig{
			Backends: map[string]config.BackendConfig{
				"primary": {Type: "mongo", Mongo: config.MongoConfig{URI: "mongodb://p", DatabaseName: "db1"}},
				"tenant1": {Type: "mongo", Mongo: config.MongoConfig{URI: "mongodb://t1", DatabaseName: "db2"}},
			},
			Topology: config.TopologyConfig{
				Document: config.DocumentTopology{BaseTopology: config.BaseTopology{Strategy: "single", Primary: "primary"}},
				User:     config.CollectionTopology{BaseTopology: config.BaseTopology{Strategy: "single", Primary: "primary"}},
				Revocation: config.CollectionTopology{BaseTopology: config.BaseTopology{Strategy: "single", Primary: "primary"}},
			},
			Tenants: map[string]config.TenantConfig{
				"t1": {Backend: "tenant1"},
			},
		},
	}

	f, err := NewFactory(context.Background(), cfg)
	require.NoError(t, err)
	defer f.Close()
}

func TestNewFactory_Errors(t *testing.T) {
	ctx := context.Background()

	t.Run("Unsupported Backend Type", func(t *testing.T) {
		cfg := &config.Config{
			Storage: config.StorageConfig{
				Backends: map[string]config.BackendConfig{
					"bad": {Type: "redis"},
				},
			},
		}
		_, err := NewFactory(ctx, cfg)
		assert.ErrorContains(t, err, "unsupported backend type")
	})

	t.Run("Document Backend Not Found", func(t *testing.T) {
		cfg := &config.Config{
			Storage: config.StorageConfig{
				Backends: map[string]config.BackendConfig{},
				Topology: config.TopologyConfig{
					Document: config.DocumentTopology{
						BaseTopology: config.BaseTopology{Primary: "missing"},
					},
				},
			},
		}
		_, err := NewFactory(ctx, cfg)
		assert.ErrorContains(t, err, "backend not found")
	})
}

func TestNewFactory_ReadWriteSplit(t *testing.T) {
	// Mock provider creation
	origNewMongoProvider := newMongoProvider
	defer func() { newMongoProvider = origNewMongoProvider }()

	newMongoProvider = func(ctx context.Context, uri, dbName string) (Provider, error) {
		client, _ := mongo.Connect(ctx, options.Client().ApplyURI(uri))
		return &mockMongoProvider{client: client, dbName: dbName}, nil
	}

	cfg := &config.Config{
		Storage: config.StorageConfig{
			Backends: map[string]config.BackendConfig{
				"primary": {
					Type: "mongo",
					Mongo: config.MongoConfig{URI: "mongodb://primary", DatabaseName: "db"},
				},
				"replica": {
					Type: "mongo",
					Mongo: config.MongoConfig{URI: "mongodb://replica", DatabaseName: "db"},
				},
			},
			Topology: config.TopologyConfig{
				Document: config.DocumentTopology{
					BaseTopology: config.BaseTopology{
						Strategy: "read_write_split",
						Primary:  "primary",
						Replica:  "replica",
					},
					DataCollection: "docs",
					SysCollection:  "sys",
				},
				User: config.CollectionTopology{
					BaseTopology: config.BaseTopology{
						Strategy: "read_write_split",
						Primary:  "primary",
						Replica:  "replica",
					},
					Collection: "users",
				},
				Revocation: config.CollectionTopology{
					BaseTopology: config.BaseTopology{
						Strategy: "read_write_split",
						Primary:  "primary",
						Replica:  "replica",
					},
					Collection: "revocations",
				},
			},
		},
	}

	ctx := context.Background()
	f, err := NewFactory(ctx, cfg)
	require.NoError(t, err)
	defer f.Close()

	assert.NotNil(t, f.Document())
	assert.NotNil(t, f.User())
	assert.NotNil(t, f.Revocation())
}
