# Transaction Implementation

## 1. Context & Requirements

While the public API of Syntrix is designed to be non-transactional (single-document atomicity only) to maximize scalability, the internal Trigger system requires multi-document ACID transactions.
Specifically, the `/api/v1/trigger/write` endpoint allows Trigger Workers to perform complex side-effects (e.g., "create a notification AND update a counter") which must succeed or fail as a unit.

## 2. Architecture

### 2.1 Storage Layer (`internal/storage`)

We extended the `StorageBackend` interface to support transactions via a closure pattern:

```go
Transaction(ctx context.Context, fn func(ctx context.Context, tx StorageBackend) error) error
```

- **MongoDB Implementation**: Uses `session.WithTransaction`.
- **Abstraction**: The `tx` argument passed to the closure is a `StorageBackend` instance bound to the transaction session.

### 2.2 Query Engine (`internal/query`)

The `Engine` exposes a `RunTransaction` method:

```go
func (e *Engine) RunTransaction(ctx context.Context, fn func(ctx context.Context, tx Service) error) error
```

- It starts a storage transaction.
- It creates a *new* `Engine` instance scoped to the transactional storage backend.
- It passes this scoped engine to the user-provided closure.
- This ensures all operations (Create, Get, Patch, Delete) called on `tx` within the closure participate in the transaction.

### 2.3 API Layer (`internal/api`)

The `/api/v1/trigger/write` handler wraps the entire batch of operations in `engine.RunTransaction`.

## 3. Testing Strategy

We implemented integration tests in `tests/integration/transaction_test.go` to verify:

1. **Commit**: Multiple writes persist when no error occurs.
2. **Rollback**: All writes are reverted if an error occurs at any point in the closure.
3. **Isolation**: Reads within the transaction see the uncommitted writes (Snapshot Isolation).
4. **Patch Rollback**: Updates to existing documents are also reverted on rollback.

## 4. Limitations

- **Replica Set Required**: MongoDB transactions only work on Replica Sets. Tests skip if transactions are not supported.
- **Time Limit**: MongoDB transactions have a 60s default timeout.
- **Sharding**: Multi-document transactions on sharded clusters have performance implications.
