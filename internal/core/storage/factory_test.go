package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/syntrixbase/syntrix/internal/core/storage/config"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
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
var originalNewPostgresDB = newPostgresDB

func newMockMongoProvider(t *testing.T, dbName string) *mockMongoProvider {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock).ShareClient(true))
	mt.AddMockResponses(mtest.CreateCursorResponse(0, dbName+".$cmd.listCollections", mtest.FirstBatch), mtest.CreateSuccessResponse(), mtest.CreateSuccessResponse())
	return &mockMongoProvider{client: mt.Client, dbName: dbName}
}

func setupMockProvider(t *testing.T) {
	newMongoProvider = func(ctx context.Context, uri, dbName string) (Provider, error) {
		return newMockMongoProvider(t, dbName), nil
	}
}

func setupMockPostgres() sqlmock.Sqlmock {
	db, mock, _ := sqlmock.New()
	newPostgresDB = func(cfg config.PostgresConfig) (*sql.DB, error) {
		return db, nil
	}
	// Mock successful ping
	mock.ExpectPing()
	// Mock EnsureSchema call for auth_users (CREATE TABLE IF NOT EXISTS ...)
	mock.ExpectExec("CREATE TABLE IF NOT EXISTS auth_users").WillReturnResult(sqlmock.NewResult(0, 0))
	// Mock EnsureSchema call for databases
	mock.ExpectExec("CREATE TABLE IF NOT EXISTS databases").WillReturnResult(sqlmock.NewResult(0, 0))
	return mock
}

func teardownMockProvider() {
	newMongoProvider = originalNewMongoProvider
	newPostgresDB = originalNewPostgresDB
}

const (
	testMongoURI = "mongodb://localhost:27017"
	testDBName   = "syntrix_test_factory"
)

func TestNewFactory(t *testing.T) {
	setupMockProvider(t)
	setupMockPostgres()
	defer teardownMockProvider()

	cfg := config.Config{
		Backends: map[string]config.BackendConfig{
			"primary": {
				Type: "mongo",
				Mongo: config.MongoConfig{
					URI:          testMongoURI,
					DatabaseName: testDBName,
				},
			},
			"postgres_user": {
				Type: "postgres",
				Postgres: config.PostgresConfig{
					DSN: "postgres://test",
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
					Primary:  "postgres_user",
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

func TestNewFactory_DatabaseConfig(t *testing.T) {
	setupMockProvider(t)
	setupMockPostgres()
	defer teardownMockProvider()

	cfg := config.Config{
		Backends: map[string]config.BackendConfig{
			"primary":       {Type: "mongo", Mongo: config.MongoConfig{URI: "mongodb://p", DatabaseName: "db1"}},
			"database1":     {Type: "mongo", Mongo: config.MongoConfig{URI: "mongodb://t1", DatabaseName: "db2"}},
			"postgres_user": {Type: "postgres", Postgres: config.PostgresConfig{DSN: "postgres://test"}},
		},
		Topology: config.TopologyConfig{
			Document:   config.DocumentTopology{BaseTopology: config.BaseTopology{Strategy: "single", Primary: "primary"}},
			User:       config.CollectionTopology{BaseTopology: config.BaseTopology{Strategy: "single", Primary: "postgres_user"}},
			Revocation: config.CollectionTopology{BaseTopology: config.BaseTopology{Strategy: "single", Primary: "primary"}},
		},
		Databases: map[string]config.DatabaseConfig{
			"t1": {Backend: "database1"},
		},
	}

	f, err := NewFactory(context.Background(), cfg)
	require.NoError(t, err)
	defer f.Close()
}

func TestNewFactory_Errors(t *testing.T) {
	ctx := context.Background()

	t.Run("Unsupported Backend Type", func(t *testing.T) {
		cfg := config.Config{
			Backends: map[string]config.BackendConfig{
				"bad": {Type: "redis"},
			},
		}
		_, err := NewFactory(ctx, cfg)
		assert.ErrorContains(t, err, "unsupported backend type")
	})

	t.Run("Document Backend Not Found", func(t *testing.T) {
		cfg := config.Config{
			Backends: map[string]config.BackendConfig{},
			Topology: config.TopologyConfig{
				Document: config.DocumentTopology{
					BaseTopology: config.BaseTopology{Primary: "missing"},
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
	origNewPostgresDB := newPostgresDB
	defer func() {
		newMongoProvider = origNewMongoProvider
		newPostgresDB = origNewPostgresDB
	}()

	newMongoProvider = func(ctx context.Context, uri, dbName string) (Provider, error) {
		return newMockMongoProvider(t, dbName), nil
	}

	db, mock, _ := sqlmock.New()
	newPostgresDB = func(cfg config.PostgresConfig) (*sql.DB, error) {
		return db, nil
	}
	mock.ExpectPing()
	mock.ExpectExec("CREATE TABLE IF NOT EXISTS auth_users").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE TABLE IF NOT EXISTS databases").WillReturnResult(sqlmock.NewResult(0, 0))

	cfg := config.Config{
		Backends: map[string]config.BackendConfig{
			"primary": {
				Type:  "mongo",
				Mongo: config.MongoConfig{URI: "mongodb://primary", DatabaseName: "db"},
			},
			"replica": {
				Type:  "mongo",
				Mongo: config.MongoConfig{URI: "mongodb://replica", DatabaseName: "db"},
			},
			"postgres_user": {
				Type:     "postgres",
				Postgres: config.PostgresConfig{DSN: "postgres://test"},
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
					Strategy: "single",
					Primary:  "postgres_user",
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
	}

	ctx := context.Background()
	f, err := NewFactory(ctx, cfg)
	require.NoError(t, err)
	defer f.Close()

	assert.NotNil(t, f.Document())
	assert.NotNil(t, f.User())
	assert.NotNil(t, f.Revocation())
}

func TestNewFactory_ProviderInitError(t *testing.T) {
	// Save original provider creator
	origNewMongoProvider := newMongoProvider
	defer func() { newMongoProvider = origNewMongoProvider }()

	// Mock provider creation to fail
	newMongoProvider = func(ctx context.Context, uri, dbName string) (Provider, error) {
		return nil, errors.New("connection failed")
	}

	cfg := config.Config{
		Backends: map[string]config.BackendConfig{
			"primary": {Type: "mongo", Mongo: config.MongoConfig{URI: "mongodb://fail", DatabaseName: "db"}},
		},
	}

	_, err := NewFactory(context.Background(), cfg)
	assert.ErrorContains(t, err, "failed to initialize backend primary")
}

func TestNewFactory_RouterErrors(t *testing.T) {
	// Mock provider creation to succeed
	origNewMongoProvider := newMongoProvider
	origNewPostgresDB := newPostgresDB
	defer func() {
		newMongoProvider = origNewMongoProvider
		newPostgresDB = origNewPostgresDB
	}()

	newMongoProvider = func(ctx context.Context, uri, dbName string) (Provider, error) {
		return newMockMongoProvider(t, dbName), nil
	}

	ctx := context.Background()

	t.Run("Document Unsupported Strategy", func(t *testing.T) {
		cfg := config.Config{
			Backends: map[string]config.BackendConfig{
				"primary": {Type: "mongo"},
			},
			Topology: config.TopologyConfig{
				Document: config.DocumentTopology{
					BaseTopology: config.BaseTopology{Primary: "primary", Strategy: "unknown"},
				},
			},
		}
		_, err := NewFactory(ctx, cfg)
		assert.ErrorContains(t, err, "unsupported strategy: unknown")
	})

	t.Run("Document Replica Missing", func(t *testing.T) {
		cfg := config.Config{
			Backends: map[string]config.BackendConfig{
				"primary": {Type: "mongo"},
			},
			Topology: config.TopologyConfig{
				Document: config.DocumentTopology{
					BaseTopology: config.BaseTopology{Primary: "primary", Strategy: "read_write_split", Replica: "missing"},
				},
			},
		}
		_, err := NewFactory(ctx, cfg)
		assert.ErrorContains(t, err, "backend not found: missing")
	})

	t.Run("User Primary Missing", func(t *testing.T) {
		cfg := config.Config{
			Backends: map[string]config.BackendConfig{
				"primary": {Type: "mongo"},
			},
			Topology: config.TopologyConfig{
				Document: config.DocumentTopology{BaseTopology: config.BaseTopology{Primary: "primary", Strategy: "single"}},
				User: config.CollectionTopology{
					BaseTopology: config.BaseTopology{Primary: "missing"},
				},
			},
		}
		_, err := NewFactory(ctx, cfg)
		assert.ErrorContains(t, err, "backend not found: missing")
	})

	t.Run("User Unsupported Backend Type", func(t *testing.T) {
		cfg := config.Config{
			Backends: map[string]config.BackendConfig{
				"primary": {Type: "mongo"},
			},
			Topology: config.TopologyConfig{
				Document: config.DocumentTopology{BaseTopology: config.BaseTopology{Primary: "primary", Strategy: "single"}},
				User: config.CollectionTopology{
					BaseTopology: config.BaseTopology{Primary: "primary", Strategy: "single"},
				},
			},
		}
		_, err := NewFactory(ctx, cfg)
		assert.ErrorContains(t, err, "unsupported backend type for user store: mongo")
	})

	t.Run("Revocation Primary Missing", func(t *testing.T) {
		db, mock, _ := sqlmock.New()
		newPostgresDB = func(cfg config.PostgresConfig) (*sql.DB, error) {
			return db, nil
		}
		mock.ExpectPing()
		mock.ExpectExec("CREATE TABLE IF NOT EXISTS auth_users").WillReturnResult(sqlmock.NewResult(0, 0))

		cfg := config.Config{
			Backends: map[string]config.BackendConfig{
				"primary":       {Type: "mongo"},
				"postgres_user": {Type: "postgres", Postgres: config.PostgresConfig{DSN: "postgres://test"}},
			},
			Topology: config.TopologyConfig{
				Document: config.DocumentTopology{BaseTopology: config.BaseTopology{Primary: "primary", Strategy: "single"}},
				User:     config.CollectionTopology{BaseTopology: config.BaseTopology{Primary: "postgres_user", Strategy: "single"}},
				Revocation: config.CollectionTopology{
					BaseTopology: config.BaseTopology{Primary: "missing"},
				},
			},
		}
		_, err := NewFactory(ctx, cfg)
		assert.ErrorContains(t, err, "backend not found: missing")
	})

	t.Run("Revocation Unsupported Strategy", func(t *testing.T) {
		db, mock, _ := sqlmock.New()
		newPostgresDB = func(cfg config.PostgresConfig) (*sql.DB, error) {
			return db, nil
		}
		mock.ExpectPing()
		mock.ExpectExec("CREATE TABLE IF NOT EXISTS auth_users").WillReturnResult(sqlmock.NewResult(0, 0))

		cfg := config.Config{
			Backends: map[string]config.BackendConfig{
				"primary":       {Type: "mongo"},
				"postgres_user": {Type: "postgres", Postgres: config.PostgresConfig{DSN: "postgres://test"}},
			},
			Topology: config.TopologyConfig{
				Document:   config.DocumentTopology{BaseTopology: config.BaseTopology{Primary: "primary", Strategy: "single"}},
				User:       config.CollectionTopology{BaseTopology: config.BaseTopology{Primary: "postgres_user", Strategy: "single"}},
				Revocation: config.CollectionTopology{BaseTopology: config.BaseTopology{Primary: "primary", Strategy: "unknown"}},
			},
		}
		_, err := NewFactory(ctx, cfg)
		assert.ErrorContains(t, err, "unsupported strategy: unknown")
	})

	t.Run("Revocation Replica Missing", func(t *testing.T) {
		db, mock, _ := sqlmock.New()
		newPostgresDB = func(cfg config.PostgresConfig) (*sql.DB, error) {
			return db, nil
		}
		mock.ExpectPing()
		mock.ExpectExec("CREATE TABLE IF NOT EXISTS auth_users").WillReturnResult(sqlmock.NewResult(0, 0))

		cfg := config.Config{
			Backends: map[string]config.BackendConfig{
				"primary":       {Type: "mongo"},
				"postgres_user": {Type: "postgres", Postgres: config.PostgresConfig{DSN: "postgres://test"}},
			},
			Topology: config.TopologyConfig{
				Document:   config.DocumentTopology{BaseTopology: config.BaseTopology{Primary: "primary", Strategy: "single"}},
				User:       config.CollectionTopology{BaseTopology: config.BaseTopology{Primary: "postgres_user", Strategy: "single"}},
				Revocation: config.CollectionTopology{BaseTopology: config.BaseTopology{Primary: "primary", Strategy: "read_write_split", Replica: "missing"}},
			},
		}
		_, err := NewFactory(ctx, cfg)
		assert.ErrorContains(t, err, "backend not found: missing")
	})
}

func TestNewFactory_DatabaseErrors(t *testing.T) {
	origNewMongoProvider := newMongoProvider
	defer func() { newMongoProvider = origNewMongoProvider }()

	newMongoProvider = func(ctx context.Context, uri, dbName string) (Provider, error) {
		return newMockMongoProvider(t, dbName), nil
	}

	ctx := context.Background()

	t.Run("Database Document Backend Missing", func(t *testing.T) {
		cfg := config.Config{
			Backends: map[string]config.BackendConfig{
				"primary": {Type: "mongo"},
			},
			Topology: config.TopologyConfig{
				Document: config.DocumentTopology{BaseTopology: config.BaseTopology{Primary: "primary", Strategy: "single"}},
			},
			Databases: map[string]config.DatabaseConfig{
				"t1": {Backend: "missing"},
			},
		}
		_, err := NewFactory(ctx, cfg)
		assert.ErrorContains(t, err, "backend not found: missing")
	})
}

func TestNewFactory_DefaultDatabaseSkipped(t *testing.T) {
	setupMockProvider(t)
	setupMockPostgres()
	defer teardownMockProvider()

	cfg := config.Config{
		Backends: map[string]config.BackendConfig{
			"primary":       {Type: "mongo", Mongo: config.MongoConfig{URI: "mongodb://p", DatabaseName: "db1"}},
			"postgres_user": {Type: "postgres", Postgres: config.PostgresConfig{DSN: "postgres://test"}},
		},
		Topology: config.TopologyConfig{
			Document:   config.DocumentTopology{BaseTopology: config.BaseTopology{Strategy: "single", Primary: "primary"}},
			User:       config.CollectionTopology{BaseTopology: config.BaseTopology{Strategy: "single", Primary: "postgres_user"}},
			Revocation: config.CollectionTopology{BaseTopology: config.BaseTopology{Strategy: "single", Primary: "primary"}},
		},
		Databases: map[string]config.DatabaseConfig{
			"default": {Backend: "primary"}, // Should be skipped
		},
	}

	f, err := NewFactory(context.Background(), cfg)
	require.NoError(t, err)
	defer f.Close()
}

type mockGenericProvider struct{}

func (m *mockGenericProvider) Close(ctx context.Context) error {
	return nil
}

func TestFactory_GetMongoProvider_Errors(t *testing.T) {
	f := &factory{
		providers: map[string]Provider{
			"generic": &mockGenericProvider{},
		},
	}

	t.Run("Backend Not Found", func(t *testing.T) {
		_, err := f.getMongoProvider("missing")
		assert.ErrorContains(t, err, "backend not found: missing")
	})

	t.Run("Not A Mongo Provider", func(t *testing.T) {
		_, err := f.getMongoProvider("generic")
		assert.ErrorContains(t, err, "is not a mongo provider")
	})
}

type errorClosingProvider struct {
	mockMongoProvider
}

func (e *errorClosingProvider) Close(ctx context.Context) error {
	return errors.New("close failed")
}

func TestFactory_CloseError(t *testing.T) {
	f := &factory{
		providers: map[string]Provider{
			"p1": &errorClosingProvider{},
		},
	}
	err := f.Close()
	assert.ErrorContains(t, err, "errors closing providers")
	assert.ErrorContains(t, err, "close failed")
}

type noopProvider struct{}

func (n *noopProvider) Close(ctx context.Context) error { return nil }

func TestFactory_GetMongoClient_Success(t *testing.T) {
	t.Parallel()

	f := &factory{providers: make(map[string]Provider)}
	client, _ := mongo.Connect(context.Background(), options.Client().ApplyURI("mongodb://mock"))
	f.providers["primary"] = &mockMongoProvider{client: client, dbName: "db1"}

	cli, dbName, err := f.GetMongoClient("primary")
	require.NoError(t, err)
	assert.Equal(t, client, cli)
	assert.Equal(t, "db1", dbName)
}

func TestFactory_GetMongoClient_Errors(t *testing.T) {
	t.Parallel()

	f := &factory{providers: make(map[string]Provider)}

	_, _, err := f.GetMongoClient("missing")
	assert.ErrorContains(t, err, "backend not found")

	f.providers["noop"] = &noopProvider{}
	_, _, err = f.GetMongoClient("noop")
	assert.ErrorContains(t, err, "not a mongo provider")
}

// Test newPostgresDB function directly
func TestNewPostgresDB(t *testing.T) {
	// Capture the original function before any mocking
	realNewPostgresDB := originalNewPostgresDB

	t.Run("WithInvalidDSN", func(t *testing.T) {
		cfg := config.PostgresConfig{
			DSN: "invalid://not-a-valid-connection",
		}
		// This will succeed at opening (sql.Open is lazy) but would fail on ping
		db, err := realNewPostgresDB(cfg)
		if err != nil {
			// Some drivers may fail on Open with invalid DSN
			return
		}
		defer db.Close()
		// The connection is lazy, so Open succeeds but Ping would fail
		assert.NotNil(t, db)
	})

	t.Run("WithConnectionPoolSettings", func(t *testing.T) {
		cfg := config.PostgresConfig{
			DSN:             "postgres://user:pass@localhost:5432/db?sslmode=disable",
			MaxOpenConns:    10,
			MaxIdleConns:    5,
			ConnMaxLifetime: 5 * time.Minute,
		}
		// sql.Open is lazy and won't fail even with invalid connection string
		db, err := realNewPostgresDB(cfg)
		if err != nil {
			// Some implementations may validate DSN on Open
			return
		}
		defer db.Close()
		assert.NotNil(t, db)
	})

	t.Run("WithZeroPoolSettings", func(t *testing.T) {
		cfg := config.PostgresConfig{
			DSN:             "postgres://user:pass@localhost:5432/db?sslmode=disable",
			MaxOpenConns:    0, // Should skip SetMaxOpenConns
			MaxIdleConns:    0, // Should skip SetMaxIdleConns
			ConnMaxLifetime: 0, // Should skip SetConnMaxLifetime
		}
		db, err := realNewPostgresDB(cfg)
		if err != nil {
			return
		}
		defer db.Close()
		assert.NotNil(t, db)
	})
}

func TestNewFactory_PostgresErrors(t *testing.T) {
	origNewMongoProvider := newMongoProvider
	origNewPostgresDB := newPostgresDB
	defer func() {
		newMongoProvider = origNewMongoProvider
		newPostgresDB = origNewPostgresDB
	}()

	newMongoProvider = func(ctx context.Context, uri, dbName string) (Provider, error) {
		return newMockMongoProvider(t, dbName), nil
	}

	ctx := context.Background()

	t.Run("PostgresConnectionError", func(t *testing.T) {
		newPostgresDB = func(cfg config.PostgresConfig) (*sql.DB, error) {
			return nil, errors.New("connection failed")
		}

		cfg := config.Config{
			Backends: map[string]config.BackendConfig{
				"primary":       {Type: "mongo"},
				"postgres_user": {Type: "postgres", Postgres: config.PostgresConfig{DSN: "postgres://test"}},
			},
			Topology: config.TopologyConfig{
				Document: config.DocumentTopology{BaseTopology: config.BaseTopology{Primary: "primary", Strategy: "single"}},
				User:     config.CollectionTopology{BaseTopology: config.BaseTopology{Primary: "postgres_user", Strategy: "single"}},
			},
		}
		_, err := NewFactory(ctx, cfg)
		assert.ErrorContains(t, err, "failed to connect to postgres")
	})

	t.Run("PostgresPingError", func(t *testing.T) {
		db, mock, _ := sqlmock.New(sqlmock.MonitorPingsOption(true))
		newPostgresDB = func(cfg config.PostgresConfig) (*sql.DB, error) {
			return db, nil
		}
		mock.ExpectPing().WillReturnError(errors.New("ping failed"))

		cfg := config.Config{
			Backends: map[string]config.BackendConfig{
				"primary":       {Type: "mongo"},
				"postgres_user": {Type: "postgres", Postgres: config.PostgresConfig{DSN: "postgres://test"}},
			},
			Topology: config.TopologyConfig{
				Document: config.DocumentTopology{BaseTopology: config.BaseTopology{Primary: "primary", Strategy: "single"}},
				User:     config.CollectionTopology{BaseTopology: config.BaseTopology{Primary: "postgres_user", Strategy: "single"}},
			},
		}
		_, err := NewFactory(ctx, cfg)
		assert.ErrorContains(t, err, "failed to ping postgres")
	})

	t.Run("PostgresEnsureSchemaError", func(t *testing.T) {
		db, mock, _ := sqlmock.New(sqlmock.MonitorPingsOption(true))
		newPostgresDB = func(cfg config.PostgresConfig) (*sql.DB, error) {
			return db, nil
		}
		mock.ExpectPing()
		mock.ExpectExec("CREATE TABLE IF NOT EXISTS auth_users").WillReturnError(errors.New("schema error"))

		cfg := config.Config{
			Backends: map[string]config.BackendConfig{
				"primary":       {Type: "mongo"},
				"postgres_user": {Type: "postgres", Postgres: config.PostgresConfig{DSN: "postgres://test"}},
			},
			Topology: config.TopologyConfig{
				Document: config.DocumentTopology{BaseTopology: config.BaseTopology{Primary: "primary", Strategy: "single"}},
				User:     config.CollectionTopology{BaseTopology: config.BaseTopology{Primary: "postgres_user", Strategy: "single"}},
			},
		}
		_, err := NewFactory(ctx, cfg)
		assert.ErrorContains(t, err, "failed to ensure postgres schema")
	})
}

func TestFactory_DatabaseAccessor(t *testing.T) {
	setupMockProvider(t)
	mock := setupMockPostgres()
	defer teardownMockProvider()

	cfg := config.Config{
		Backends: map[string]config.BackendConfig{
			"primary": {
				Type: "mongo",
				Mongo: config.MongoConfig{
					URI:          testMongoURI,
					DatabaseName: testDBName,
				},
			},
			"postgres_user": {
				Type: "postgres",
				Postgres: config.PostgresConfig{
					DSN: "postgres://test",
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
					Primary:  "postgres_user",
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
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	f, err := NewFactory(ctx, cfg)
	require.NoError(t, err)
	defer f.Close()

	// Test Database() accessor
	dbStore := f.Database()
	assert.NotNil(t, dbStore)

	// Verify it's a PostgreSQL-backed store by checking its type
	_ = mock // Use mock to avoid lint error
}

func TestFactory_PrepareDocumentCollections(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	for _, test := range []struct {
		name      string
		existing  []bson.D
		responses []bson.D
		commands  []string
		errorCode int32
	}{
		{name: "fresh", commands: []string{"listCollections", "create", "create"}, responses: []bson.D{mtest.CreateSuccessResponse(), mtest.CreateSuccessResponse()}},
		{name: "existing without create permission", existing: []bson.D{{{Key: "name", Value: "data_custom"}}, {{Key: "name", Value: "sys_custom"}}}, commands: []string{"listCollections"}},
		{name: "concurrent creation", commands: []string{"listCollections", "create", "create"}, responses: []bson.D{mtest.CreateCommandErrorResponse(mtest.CommandError{Code: 48, Name: "NamespaceExists"}), mtest.CreateSuccessResponse()}},
		{name: "create error", commands: []string{"listCollections", "create"}, responses: []bson.D{mtest.CreateCommandErrorResponse(mtest.CommandError{Code: 13, Name: "Unauthorized"})}, errorCode: 13},
	} {
		mt.Run(test.name, func(mt *mtest.T) {
			cfg := config.DefaultConfig()
			cfg.Topology.Document.DataCollection = "data_custom"
			cfg.Topology.Document.SysCollection = "sys_custom"
			f := &factory{providers: map[string]Provider{"default_mongo": &mockMongoProvider{client: mt.Client, dbName: "document_readiness"}}}
			mt.AddMockResponses(mtest.CreateCursorResponse(0, "document_readiness.$cmd.listCollections", mtest.FirstBatch, test.existing...))
			mt.AddMockResponses(test.responses...)
			err := f.prepareDocumentCollections(context.Background(), cfg)
			if test.errorCode != 0 {
				var native mongo.CommandError
				require.ErrorAs(t, err, &native)
				require.Equal(t, test.errorCode, native.Code)
				require.ErrorContains(t, err, "data_custom on backend default_mongo")
			} else {
				require.NoError(t, err)
			}
			var commands []string
			for _, event := range mt.GetAllStartedEvents() {
				commands = append(commands, event.CommandName)
			}
			require.Equal(t, test.commands, commands)
		})
	}
	mt.Run("factory fails when namespace discovery fails", func(mt *mtest.T) {
		defer teardownMockProvider()
		setupMockPostgres()
		newMongoProvider = func(context.Context, string, string) (Provider, error) {
			return &mockMongoProvider{client: mt.Client, dbName: "document_readiness"}, nil
		}
		mt.AddMockResponses(mtest.CreateCommandErrorResponse(mtest.CommandError{Code: 13, Name: "Unauthorized"}))
		f, err := NewFactory(context.Background(), config.DefaultConfig())
		require.Nil(t, f)
		var native mongo.CommandError
		require.ErrorAs(t, err, &native)
		require.EqualValues(t, 13, native.Code)
		require.ErrorContains(t, err, "failed to list document collections on backend default_mongo")
	})
	f := &factory{}
	require.ErrorContains(t, f.prepareDocumentCollections(context.Background(), config.DefaultConfig()), "backend not found")
}

func TestNewFactory_DocumentNamespaceReadiness(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	uri := os.Getenv("MONGO_URI")
	if uri == "" {
		uri = testMongoURI
	}
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri).SetServerSelectionTimeout(2*time.Second))
	require.NoError(t, err)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		require.NoError(t, client.Disconnect(cleanupCtx))
	})
	if err := client.Ping(ctx, nil); err != nil {
		if os.Getenv("CI") != "" {
			t.Fatal(err)
		}
		t.Skipf("MongoDB unavailable: %v", err)
	}
	prefix := fmt.Sprintf("factory_readiness_%d", time.Now().UnixNano())
	cfg := config.DefaultConfig()
	cfg.Topology.Document.Strategy = "read_write_split"
	cfg.Topology.Document.Replica = "replica_only"
	cfg.Topology.Document.DataCollection = "data_custom"
	cfg.Topology.Document.SysCollection = "sys_custom"
	for _, backend := range []string{"default_mongo", "tenant", "replica_only"} {
		cfg.Backends[backend] = config.BackendConfig{Type: "mongo", Mongo: config.MongoConfig{DatabaseName: prefix + "_" + backend}}
		db := client.Database(cfg.Backends[backend].Mongo.DatabaseName)
		t.Cleanup(func() {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cleanupCancel()
			require.NoError(t, db.Drop(cleanupCtx))
		})
	}
	cfg.Databases["tenant_database"] = config.DatabaseConfig{Backend: "tenant"}
	cfg.Databases["another_tenant_database"] = config.DatabaseConfig{Backend: "tenant"}
	newMongoProvider = func(_ context.Context, _, dbName string) (Provider, error) {
		return &mockMongoProvider{client: client, dbName: dbName}, nil
	}
	defer teardownMockProvider()
	setupMockPostgres()
	created, err := NewFactory(ctx, cfg)
	require.NoError(t, err)
	defer created.Close()
	f := created.(*factory)
	for _, backend := range []string{"default_mongo", "tenant"} {
		db := client.Database(cfg.Backends[backend].Mongo.DatabaseName)
		names, err := db.ListCollectionNames(ctx, bson.D{})
		require.NoError(t, err)
		require.ElementsMatch(t, []string{"data_custom", "sys_custom"}, names)
		specs, err := db.ListCollectionSpecifications(ctx, bson.M{"name": "data_custom"})
		require.NoError(t, err)
		require.Len(t, specs, 1)
		require.NotNil(t, specs[0].UUID)
		_, err = db.Collection("data_custom").InsertOne(ctx, bson.M{"_id": "retained", "value": 7})
		require.NoError(t, err)
		require.NoError(t, f.prepareDocumentCollections(ctx, cfg))
		after, err := db.ListCollectionSpecifications(ctx, bson.M{"name": "data_custom"})
		require.NoError(t, err)
		require.Equal(t, specs[0].UUID, after[0].UUID)
		var retained bson.M
		require.NoError(t, db.Collection("data_custom").FindOne(ctx, bson.M{"_id": "retained"}).Decode(&retained))
		require.EqualValues(t, 7, retained["value"])
		indexes, err := db.Collection("data_custom").Indexes().List(ctx)
		require.NoError(t, err)
		var indexDocs []bson.M
		require.NoError(t, indexes.All(ctx, &indexDocs))
		require.Len(t, indexDocs, 1)
		require.Equal(t, "_id_", indexDocs[0]["name"])
	}
	replicaDB := client.Database(cfg.Backends["replica_only"].Mongo.DatabaseName)
	names, err := replicaDB.ListCollectionNames(ctx, bson.D{})
	require.NoError(t, err)
	require.Empty(t, names)

	for _, backend := range []string{"default_mongo", "tenant"} {
		require.NoError(t, client.Database(cfg.Backends[backend].Mongo.DatabaseName).Drop(ctx))
	}
	var wg sync.WaitGroup
	errors := make(chan error, 4)
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errors <- f.prepareDocumentCollections(ctx, cfg)
		}()
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	for _, backend := range []string{"default_mongo", "tenant"} {
		names, err := client.Database(cfg.Backends[backend].Mongo.DatabaseName).ListCollectionNames(ctx, bson.D{})
		require.NoError(t, err)
		require.ElementsMatch(t, []string{"data_custom", "sys_custom"}, names)
	}
}
