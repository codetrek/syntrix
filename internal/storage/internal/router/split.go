package router

import (
	"github.com/codetrek/syntrix/internal/storage/types"
)

// SplitDocumentRouter routes read operations to replica and write operations to primary
type SplitDocumentRouter struct {
	primary types.DocumentStore
	replica types.DocumentStore
}

func NewSplitDocumentRouter(primary, replica types.DocumentStore) types.DocumentRouter {
	return &SplitDocumentRouter{primary: primary, replica: replica}
}

func (r *SplitDocumentRouter) Select(op types.OpKind) types.DocumentStore {
	if op == types.OpRead {
		return r.replica
	}
	return r.primary
}

// SplitUserRouter routes read operations to replica and write operations to primary
type SplitUserRouter struct {
	primary types.UserStore
	replica types.UserStore
}

func NewSplitUserRouter(primary, replica types.UserStore) types.UserRouter {
	return &SplitUserRouter{primary: primary, replica: replica}
}

func (r *SplitUserRouter) Select(op types.OpKind) types.UserStore {
	if op == types.OpRead {
		return r.replica
	}
	return r.primary
}

// SplitRevocationRouter routes read operations to replica and write operations to primary
type SplitRevocationRouter struct {
	primary types.TokenRevocationStore
	replica types.TokenRevocationStore
}

func NewSplitRevocationRouter(primary, replica types.TokenRevocationStore) types.RevocationRouter {
	return &SplitRevocationRouter{primary: primary, replica: replica}
}

func (r *SplitRevocationRouter) Select(op types.OpKind) types.TokenRevocationStore {
	if op == types.OpRead {
		return r.replica
	}
	return r.primary
}
