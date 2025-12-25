# Storage Interfaces & Data Model

## 1. Store Interfaces

The application interacts with storage through these high-level interfaces, agnostic of the underlying backend or routing topology.

### 1.1. DocumentStore
Handles generic JSON document storage with path-based addressing.

```go
type DocumentStore interface {
    Get(ctx context.Context, path string) (*Document, error)
    Create(ctx context.Context, doc *Document) error
    Update(ctx context.Context, path string, data map[string]interface{}, pred model.Filters) error
    Patch(ctx context.Context, path string, data map[string]interface{}, pred model.Filters) error
    Delete(ctx context.Context, path string, pred model.Filters) error
    Query(ctx context.Context, q model.Query) ([]*Document, error)
    Watch(ctx context.Context, collection string, resumeToken interface{}, opts WatchOptions) (<-chan Event, error)
    Close(ctx context.Context) error
}
```

### 1.2. UserStore
Handles user identity and profile data.

```go
type UserStore interface {
    CreateUser(ctx context.Context, user *User) error
    GetUserByUsername(ctx context.Context, username string) (*User, error)
    GetUserByID(ctx context.Context, id string) (*User, error)
    ListUsers(ctx context.Context, limit int, offset int) ([]*User, error)
    UpdateUser(ctx context.Context, user *User) error
    UpdateUserLoginStats(ctx context.Context, id string, lastLogin time.Time, attempts int, lockoutUntil time.Time) error
    EnsureIndexes(ctx context.Context) error
    Close(ctx context.Context) error
}
```

### 1.3. TokenRevocationStore
Handles JWT revocation lists (blacklists).

```go
type TokenRevocationStore interface {
    RevokeToken(ctx context.Context, jti string, expiresAt time.Time) error
    RevokeTokenImmediate(ctx context.Context, jti string, expiresAt time.Time) error
    IsRevoked(ctx context.Context, jti string, gracePeriod time.Duration) (bool, error)
    EnsureIndexes(ctx context.Context) error
    Close(ctx context.Context) error
}
```

## 2. Data Model (Internal)

While interfaces are generic, the internal data model (especially for the default Mongo backend) has specific structures.

### 2.1. Document Schema
- **_id**: 128-bit BLAKE3 hash of `fullpath` (Binary). Ensures uniform distribution.
- **fullpath**: String. Unique index.
- **collection**: String. Parent collection name. Shard key (for locality).
- **parent**: String. Parent document path.
- **data**: Map. The actual JSON content.
- **version**: Int64. Optimistic concurrency control.
- **updated_at**: Int64. Timestamp.
- **created_at**: Int64. Timestamp.
- **deleted**: Boolean. Soft delete flag.

### 2.2. User Schema
- **_id**: String (UUID).
- **username**: String. Unique index.
- **password_hash**: String.
- **roles**: Array of Strings.
- **profile**: Map. Arbitrary user data.

## 3. RoutedStore (The Facade)

The `RoutedStore` is a concrete implementation of the Store interfaces that acts as a proxy.

- **Responsibility**: It does not execute DB logic itself. It delegates to a `Router` to find the correct underlying Store for the operation.
- **Example**:
  ```go
  func (s *RoutedDocumentStore) Get(ctx, path) {
      // 1. Ask Router for the store handling Read operations
      store := s.router.Select(OpRead)
      // 2. Delegate execution
      return store.Get(ctx, path)
  }
  ```
