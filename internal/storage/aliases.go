package storage

import (
	"github.com/codetrek/syntrix/internal/storage/types"
)

type Document = types.Document
type User = types.User
type RevokedToken = types.RevokedToken
type DocumentStore = types.DocumentStore
type UserStore = types.UserStore
type TokenRevocationStore = types.TokenRevocationStore
type DocumentProvider = types.DocumentProvider
type AuthProvider = types.AuthProvider
type OpKind = types.OpKind
type EventType = types.EventType
type Event = types.Event
type ReplicationPullRequest = types.ReplicationPullRequest
type ReplicationPullResponse = types.ReplicationPullResponse
type ReplicationPushChange = types.ReplicationPushChange
type ReplicationPushRequest = types.ReplicationPushRequest
type ReplicationPushResponse = types.ReplicationPushResponse
type WatchOptions = types.WatchOptions
type Router = types.Router
type DocumentRouter = types.DocumentRouter
type UserRouter = types.UserRouter
type RevocationRouter = types.RevocationRouter

const (
	OpRead    = types.OpRead
	OpWrite   = types.OpWrite
	OpMigrate = types.OpMigrate
)

const (
	EventCreate = types.EventCreate
	EventUpdate = types.EventUpdate
	EventDelete = types.EventDelete
)

var (
	ErrUserNotFound = types.ErrUserNotFound
	ErrUserExists   = types.ErrUserExists
)
