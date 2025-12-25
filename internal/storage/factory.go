package storage

import (
	"context"

	"github.com/codetrek/syntrix/internal/config"
	"github.com/codetrek/syntrix/internal/storage/internal/mongo"
)

// NewDocumentProvider creates a new document provider
func NewDocumentProvider(ctx context.Context, cfg config.StorageConfig) (DocumentProvider, error) {
	// Currently only supports Mongo
	return mongo.NewDocumentProvider(ctx, cfg.Document.Mongo.URI, cfg.Document.Mongo.DatabaseName, cfg.Document.Mongo.DataCollection, cfg.Document.Mongo.SysCollection, cfg.Document.Mongo.SoftDeleteRetention)
}

// NewAuthProvider creates a new auth provider
func NewAuthProvider(ctx context.Context, cfg config.StorageConfig) (AuthProvider, error) {
	// Currently only supports Mongo
	// Note: We use User config for both User and Revocation for now as they share the same provider in Mongo implementation
	return mongo.NewAuthProvider(ctx, cfg.User.Mongo.URI, cfg.User.Mongo.DatabaseName)
}
