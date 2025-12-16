# Query Engine Module

**Package**: `internal/query`

The Query Engine is the core logic layer for data access. It provides an abstraction over the raw storage backend.

## Components

### 1. Service Interface (`interface.go`)
Defines the contract for data operations:
-   `GetDocument`
-   `CreateDocument`
-   `UpdateDocument`
-   `ReplaceDocument`
-   `PatchDocument`
-   `DeleteDocument`
-   `QueryDocuments`
-   `WatchCollection`

### 2. Engine (`engine.go`)
The local implementation of `Service`.
-   **Direct Storage Access**: Connects directly to `storage.StorageBackend`.
-   **Logic**: Handles business logic like "Patch" (Read-Modify-Write with CAS) and "Replace" (Upsert).

### 3. Client (`client.go`)
The remote implementation of `Service`.
-   **RPC/HTTP**: Forwards requests to a remote Query Service instance via HTTP.
-   **Usage**: Used by API Gateway or Realtime Service when they are deployed separately from the Query Engine.

## Key Features

-   **CAS (Compare-And-Swap)**: `PatchDocument` implements optimistic concurrency control using document versions.
-   **Upsert**: `ReplaceDocument` handles "create if not exists, update if exists" logic.
