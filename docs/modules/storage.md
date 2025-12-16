# Storage Module

**Package**: `internal/storage`

The Storage module defines the data model and provides the interface for the underlying database.

## Data Model

### Document (`types.go`)
-   **Id**: Unique identifier (path-like).
-   **Collection**: Parent collection name.
-   **Data**: JSON-like map (`map[string]interface{}`).
-   **Version**: Integer for Optimistic Concurrency Control (OCC).
-   **UpdatedAt**: Timestamp.

### Event (`types.go`)
Represents a change in the system:
-   **Type**: `create`, `update`, `delete`.
-   **Document**: The state of the document (post-image).
-   **Path**: Document path.

## Backend Interface

`StorageBackend` interface defines:
-   `Get`, `Create`, `Update`, `Delete`
-   `Query`: Complex queries with filters and sorting.
-   `Watch`: Realtime change stream.

## MongoDB Implementation (`internal/storage/mongo`)

-   **Mapping**: Maps `Document` struct to MongoDB BSON.
-   **CAS**: Uses `update({ _id: ..., version: old_ver }, { $set: ..., $inc: { version: 1 } })` to ensure atomic updates.
-   **Change Stream**: Wraps MongoDB's `Watch` API to produce a Go channel of `Event` objects.
