package storage

import (
	"github.com/codetrek/syntrix/internal/storage/types"
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

	// Close closes all underlying providers and connections.
	Close() error
}
