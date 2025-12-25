# Storage Providers (Physical Layer)

## 1. Concept

A **Provider** represents a handle to a physical storage backend (e.g., a MongoDB Cluster, a Postgres Database). It is responsible for the lifecycle of the connection and the initialization of the schema/indexes.

## 2. Responsibilities

1.  **Connection Management**:
    - Holds the low-level client (e.g., `*mongo.Client`).
    - Manages connection pools, timeouts, and keep-alives.
2.  **Lifecycle**:
    - `Connect(ctx)`: Establish connection.
    - `Close(ctx)`: Gracefully shutdown connection.
3.  **Initialization**:
    - `EnsureIndexes(ctx)`: Create required indexes on startup.
    - Schema migrations (if applicable).
4.  **Factory**:
    - Produces `Store` instances that use this connection.

## 3. Interfaces

```go
// DocumentProvider manages the backend for documents
type DocumentProvider interface {
    Document() DocumentStore
    Close(ctx context.Context) error
}

// AuthProvider manages the backend for users and revocations
type AuthProvider interface {
    Users() UserStore
    Revocations() TokenRevocationStore
    Close(ctx context.Context) error
}
```

## 4. Implementations

### 4.1. MongoProvider
- **Config**: URI, Database Name, Collection Names.
- **Behavior**:
  - Connects using official Go driver.
  - Pings on startup to verify connectivity.
  - Creates `documentStore` struct which holds the `*mongo.Collection`.

### 4.2. Future Providers
- **PostgresProvider**: For relational user data.
- **RedisProvider**: For high-performance token revocation lists.
- **MemoryProvider**: For local testing and ephemeral storage.
