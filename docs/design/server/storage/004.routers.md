# Storage Routing Strategy

## 1. Concept

Routing decouples the **intent** of an operation (Read/Write) from the **target** execution unit (Primary/Replica). This allows the topology to change without modifying application logic.

## 2. Router Interfaces

Each store type has a corresponding Router interface.

```go
type OpKind int

const (
    OpRead OpKind = iota
    OpWrite
    OpMigrate // For future dual-write scenarios
)

type DocumentRouter interface {
    Select(op OpKind) DocumentStore
}

type UserRouter interface {
    Select(op OpKind) UserStore
}

type RevocationRouter interface {
    Select(op OpKind) TokenRevocationStore
}
```

## 3. Strategies

### 3.1. Single Strategy (Default)
- **Behavior**: Returns the same Store instance for all `OpKind`.
- **Use Case**: Single node DB, or simple Replica Set where driver handles read preference.
- **Config**:
  ```yaml
  routing:
    strategy: "single"
  ```

### 3.2. Read/Write Split Strategy
- **Behavior**:
  - `OpRead` -> Returns `ReplicaStore` (or Round Robin of replicas).
  - `OpWrite` -> Returns `PrimaryStore`.
- **Use Case**: High read traffic scaling.
- **Config**:
  ```yaml
  routing:
    strategy: "read_write_split"
  ```

### 3.3. Sharding Strategy (Future)
- **Behavior**: Inspects the query/path to determine the shard, then selects the store.
- **Note**: This might require extending `Select` to accept context/parameters, or handling sharding at the Provider level (if the DB supports it natively like Mongo Cluster).

## 4. Implementation Details

- **Stateless**: Routers should generally be stateless or immutable after initialization.
- **Performance**: `Select` is on the hot path. It must be extremely fast (no I/O, just pointer return).
