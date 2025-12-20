package query_test

import (
	"context"
	"os"
	"testing"
	"time"

	"syntrix/internal/query"
	"syntrix/internal/storage"
	"syntrix/internal/storage/mongo"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTransactionIntegration(t *testing.T) {
	mongoURI := os.Getenv("MONGO_URI")
	if mongoURI == "" {
		mongoURI = "mongodb://localhost:27017"
	}
	dbName := "syntrix_transaction_test"

	ctx := context.Background()
	connCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// 1. Setup Backend
	backend, err := mongo.NewMongoBackend(connCtx, mongoURI, dbName, "documents", "sys")
	if err != nil {
		t.Skipf("Skipping integration test: could not connect to MongoDB: %v", err)
	}
	defer backend.Close(context.Background())

	// Check if transactions are supported (Replica Set required)
	// We can try a dummy transaction to check support
	err = backend.Transaction(ctx, func(ctx context.Context, tx storage.StorageBackend) error {
		return nil
	})
	if err != nil {
		t.Skipf("Skipping transaction test: MongoDB transactions not supported (requires Replica Set): %v", err)
	}

	// Clean up
	backend.DB().Drop(ctx)

	engine := query.NewEngine(backend, "http://localhost:8080")

	t.Run("Successful Transaction", func(t *testing.T) {
		err := engine.RunTransaction(ctx, func(ctx context.Context, tx query.Service) error {
			doc1 := storage.NewDocument("tx/doc1", "tx", map[string]interface{}{"val": 1})
			if err := tx.CreateDocument(ctx, doc1); err != nil {
				return err
			}

			doc2 := storage.NewDocument("tx/doc2", "tx", map[string]interface{}{"val": 2})
			if err := tx.CreateDocument(ctx, doc2); err != nil {
				return err
			}
			return nil
		})
		require.NoError(t, err)

		// Verify
		doc1, err := engine.GetDocument(ctx, "tx/doc1")
		assert.NoError(t, err)
		assert.NotNil(t, doc1)

		doc2, err := engine.GetDocument(ctx, "tx/doc2")
		assert.NoError(t, err)
		assert.NotNil(t, doc2)
	})

	t.Run("Rollback Transaction", func(t *testing.T) {
		err := engine.RunTransaction(ctx, func(ctx context.Context, tx query.Service) error {
			doc3 := storage.NewDocument("tx/doc3", "tx", map[string]interface{}{"val": 3})
			if err := tx.CreateDocument(ctx, doc3); err != nil {
				return err
			}

			// Simulate error
			return assert.AnError
		})
		require.Error(t, err)

		// Verify doc3 does NOT exist
		_, err = engine.GetDocument(ctx, "tx/doc3")
		assert.Equal(t, storage.ErrNotFound, err)
	})

	t.Run("Rollback Patch", func(t *testing.T) {
		// Setup initial doc
		doc5 := storage.NewDocument("tx/doc5", "tx", map[string]interface{}{"val": 5})
		require.NoError(t, engine.CreateDocument(ctx, doc5))

		err := engine.RunTransaction(ctx, func(ctx context.Context, tx query.Service) error {
			_, err := tx.PatchDocument(ctx, "tx/doc5", map[string]interface{}{"val": 55}, storage.Filters{})
			if err != nil {
				return err
			}
			return assert.AnError
		})
		require.Error(t, err)

		// Verify doc5 is still 5
		doc5, err = engine.GetDocument(ctx, "tx/doc5")
		assert.NoError(t, err)
		assert.EqualValues(t, 5, doc5.Data["val"])
	})

	t.Run("Nested Operations in Transaction", func(t *testing.T) {
		err := engine.RunTransaction(ctx, func(ctx context.Context, tx query.Service) error {
			// Create
			doc4 := storage.NewDocument("tx/doc4", "tx", map[string]interface{}{"val": 4})
			if err := tx.CreateDocument(ctx, doc4); err != nil {
				return err
			}

			// Read back within transaction (Snapshot isolation)
			readDoc, err := tx.GetDocument(ctx, "tx/doc4")
			if err != nil {
				return err
			}

			// Handle number type differences (int vs float64 from Mongo)
			val := readDoc.Data["val"]
			isEqual := false
			switch v := val.(type) {
			case int:
				isEqual = v == 4
			case int32:
				isEqual = v == 4
			case int64:
				isEqual = v == 4
			case float64:
				isEqual = v == 4.0
			}

			if !isEqual {
				t.Logf("Expected 4, got %v (type %T)", val, val)
				return assert.AnError
			}

			// Update within transaction
			_, err = tx.PatchDocument(ctx, "tx/doc4", map[string]interface{}{"val": 44}, storage.Filters{})
			if err != nil {
				return err
			}

			return nil
		})
		require.NoError(t, err)

		// Verify final state
		doc4, err := engine.GetDocument(ctx, "tx/doc4")
		assert.NoError(t, err)
		assert.EqualValues(t, 44, doc4.Data["val"])
	})
}
