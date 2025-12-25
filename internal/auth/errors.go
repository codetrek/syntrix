package auth

import (
	"github.com/codetrek/syntrix/internal/storage"
)

var (
	ErrUserNotFound = storage.ErrUserNotFound
	ErrUserExists   = storage.ErrUserExists
)
