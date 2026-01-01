package storage

import (
	"github.com/codetrek/syntrix/internal/storage/types"
	"go.mongodb.org/mongo-driver/mongo"
)

// StorageFactory defines the interface for creating and retrieving storage stores.
// It abstracts the underlying topology and provider management.
type StorageFactory interface {
	// Document returns the document store.
	Document() types.DocumentStore

	// User returns the user store.
	User() types.UserStore

	// Revocation returns the token revocation store.
	Revocation() types.TokenRevocationStore

	// GetMongoClient returns the raw MongoDB client for a given backend name.
	// This is used by services that need direct access to the database (e.g. Puller).
	GetMongoClient(name string) (*mongo.Client, string, error)

	// Close closes all underlying providers and connections.
	Close() error
}
