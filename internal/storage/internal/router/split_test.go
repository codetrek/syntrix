package router

import (
	"testing"

	"github.com/codetrek/syntrix/internal/storage/types"
	"github.com/stretchr/testify/assert"
)

type mockDocStore struct {
	types.DocumentStore
	id string
}

type mockUserStore struct {
	types.UserStore
	id string
}

type mockRevStore struct {
	types.TokenRevocationStore
	id string
}

func TestSplitDocumentRouter(t *testing.T) {
	primary := &mockDocStore{id: "primary"}
	replica := &mockDocStore{id: "replica"}
	router := NewSplitDocumentRouter(primary, replica)

	assert.Equal(t, replica, router.Select(types.OpRead))
	assert.Equal(t, primary, router.Select(types.OpWrite))
}

func TestSplitUserRouter(t *testing.T) {
	primary := &mockUserStore{id: "primary"}
	replica := &mockUserStore{id: "replica"}
	router := NewSplitUserRouter(primary, replica)

	assert.Equal(t, replica, router.Select(types.OpRead))
	assert.Equal(t, primary, router.Select(types.OpWrite))
}

func TestSplitRevocationRouter(t *testing.T) {
	primary := &mockRevStore{id: "primary"}
	replica := &mockRevStore{id: "replica"}
	router := NewSplitRevocationRouter(primary, replica)

	assert.Equal(t, replica, router.Select(types.OpRead))
	assert.Equal(t, primary, router.Select(types.OpWrite))
}
