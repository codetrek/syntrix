# Storage & Query Engine Discussion

**Date:** December 13, 2025
**Topic:** Storage Interface, Data Model, and Query DSL

## 1. Storage Interface (Go)

We need a `StorageBackend` interface that abstracts the underlying database (MongoDB). This allows us to potentially switch backends in the future and makes testing easier.

```go
package storage

import (
	"context"
)

// Document represents a stored document
type Document struct {
	// Id is the unique identifier for the document, 128-bit BLAKE3 of fullpath, binary
	Id string `json:"id" bson:"_id"`

	// Fullpath is the Full Pathname of document
	Fullpath string `json:"fullpath" bson:"fullpath"`

	// Collection is the parent collection name
	Collection string `json:"collection" bson:"collection"`

	// Parent is the parent of collection
	Parent string `json:"parent" bson:"parent"`

	// Data is the actual content of the document
	Data map[string]interface{} `json:"data" bson:"data"`

	// UpdatedAt is the timestamp of the last update (Unix millionseconds)
	UpdatedAt int64 `json:"updatedAt" bson:"updatedAt"`

	// CreatedAt is the timestamp of the creation (Unix millionseconds)
	CreatedAt int64 `json:"createdAt" bson:"createdAt"`

	// Version is the optimistic concurrency control version
	Version int64 `json:"version" bson:"version"`
}

// StorageBackend defines the interface for storage operations
type StorageBackend interface {
	// Document Operations
	Get(ctx context.Context, path string) (*Document, error)
	Set(ctx context.Context, path string, data map[string]interface{}) error
	Delete(ctx context.Context, path string) error

	// Query Operations
	Query(ctx context.Context, q Query) (Iterator, error)

	// Realtime (Low-level change stream)
	Watch(ctx context.Context, collection string) (<-chan ChangeEvent, error)

	// Lifecycle
	Close() error
}

type Iterator interface {
	Next() (*Document, error)
	Close() error
}

type ChangeEvent struct {
	Type      string // "insert", "update", "delete"
	Path      string
	Document  *Document
	Timestamp int64
}

// Watch semantics:
// - Backpressure: iterator may block when consumer is slow; caller should cancel ctx to drop the watch and free resources.
// - Cancelation: ctx cancel propagates to the change stream subscription and closes the channel with a terminal error.
// - Buffering: implementations may buffer a small, bounded number of events; if overflow occurs, return an explicit backpressure error so callers can resubscribe or slow down.
```

## 2. Data Model (MongoDB)

We will use a **Single Collection** strategy for simplicity and efficiency.

**Collection Name:** `documents`

**Schema:**
```javascript
{
  "_id": "binary_blake3_hash",              // Primary Key: 128-bit BLAKE3 of fullpath
  "fullpath": "users/alice/posts/post1",      // Full Pathname
  "collection": "users/alice/posts",          // Collection fullpath (for listing documents)
  "parent": "users/alice",                    // Parent path (parent of the collection)
  "updatedAt": NumberLong(1678888888000),    // Timestamp
  "createdAt": NumberLong(1678888888000),    // Timestamp
  "version": NumberLong(1),                   // Optimistic locking version
  "data": {                                   // Actual user data
    "title": "Hello World",
    "content": "..."
  }
}
```

**Sharding Strategy:**
- **Shard Key**: `collection`.
- **Rationale**: Optimizes for read performance (Data Locality). Queries for a specific collection (e.g., "messages in room A") hit a single shard.
- **Trade-off**: Potential write hotspots for massive collections. Accepted for initial design.

**Indexes:**
1.  `_id`: Default unique index.
2.  `collection`: For listing documents in a collection (`db.documents.find({collection: "users/alice/posts"})`).
3.  `parent`: For hierarchical queries (optional).
4.  **Composite indexes**:
    - **Manual Creation**: Indexes must be created manually.
    - **Strict Mode**: Queries missing required indexes will **fail** and return an error suggesting the required index.

## 3. Query DSL

We need a structured way to represent queries that can be serialized (JSON) and passed from Client -> API -> Query Engine.

```go
type Query struct {
	Collection string   // e.g., "users/alice/posts" (full path to the collection)
	Filters    []Filter
	OrderBy    []Order
	Limit      int
	StartAfter interface{} // Cursor for pagination
}

type Filter struct {
	Field string      // e.g., "status"
	Op    string      // "==", ">", ">=", "<", "<=", "in", "array-contains"
	Value interface{} // "active"
}

type Order struct {
	Field     string // "createdAt"
	Direction string // "asc" or "desc"
}
```

### Query Translation (Example)

**Syntrix Query:**
```json
{
  "collection": "users/alice/posts",
  "filters": [
    {"field": "status", "op": "==", "value": "published"},
    {"field": "views", "op": ">", "value": 100}
  ],
  "orderBy": [{"field": "views", "direction": "desc"}],
  "limit": 10
}
```

**MongoDB Aggregation / Find:**
```javascript
db.documents.find({
  "_parent": "users/alice/posts",
  "data.status": "published",
  "data.views": { "$gt": 100 }
}).sort({
  "data.views": -1
}).limit(10)
```

## 4. Open Questions

1.  **Subcollections**: How do we handle deep nesting?
    *   *Proposal*: The `_parent` field handles this naturally. `users/alice` has parent `users`. `users/alice/posts/p1` has parent `users/alice/posts`.
2.  **Array Contains**: How to support `array-contains` queries efficiently?
    *   *Proposal*: MongoDB supports this natively. We just need to map the operator.
3.  **Composite Indexes**: Do we require users to define indexes manually for complex queries (like Firestore)?
    *   *Proposal*: Yes, initially. Auto-indexing is complex. We should fail queries that require a missing index.

## 5. Realtime Matching Strategy Analysis

### 5.1 The Challenge: "Live Query" Matching
The core problem is matching a high volume of **Document Change Events** (from MongoDB Change Streams) to thousands of active **User Subscriptions** (Queries).

**Key Difficulties:**
1.  **Matching Efficiency**: With 100k+ concurrent subscriptions, we cannot iterate through all of them for every database write.
2.  **"Enter/Leave" View Problem**:
    *   **Enter**: A document is updated and now matches a query (e.g., `status` changed to "published").
    *   **Leave**: A document is updated and no longer matches.
    *   **Move**: A document's sort order changes.
3.  **The "Limit" Complexity**: If a user subscribes to `Limit: 50`, and a new document is inserted at position 1, the document at position 50 falls out of the view. The server needs to know *which* document fell out to notify the client, which implies maintaining the current result set state for every subscription.

### 5.2 Alternatives Considered

#### Option A: Server-Side Sliding Window (Stateful)
The server maintains a "Boundary Value" (e.g., the timestamp of the 50th message) for every subscription.
*   **Logic**: Only push updates if `NewDoc.Timestamp > Boundary`.
*   **Pros**: Saves bandwidth by not sending irrelevant historical updates.
*   **Cons**:
    *   **High Complexity**: Handling deletions is hard (if #49 is deleted, we need to fetch #51 from DB to maintain the window).
    *   **Stateful**: Increases memory usage per connection.

#### Option B: Push-All-Matching (Stateless / Client-Side Limit)
The server ignores `Limit` and `OrderBy` during the streaming phase. It only evaluates `Filters`.
*   **Logic**: If `Doc` matches `Filters`, push it.
*   **Pros**:
    *   **Stateless**: Server doesn't need to know the current state of the client's view.
    *   **Simple**: Matching is reduced to a boolean check (or hash lookup for equality).
    *   **Leverages Client**: Smart clients like RxDB are designed to ingest a stream of updates and maintain the sorted/limited view locally.

### 5.3 Decision: Push-All-Matching

We chose **Option B** for the initial architecture.

**Implementation Details:**
1.  **Initial Snapshot**: The server executes the full query (Filter + Sort + Limit) and returns the initial result set to populate the client.
2.  **Streaming Updates**:
    *   The server **ignores** `Limit` and `OrderBy`.
    *   The server pushes **all** document changes that match the `Filters`.
    *   **Upsert Semantics**: We treat updates as "Upserts". If a document matches the filter, we push it. The client determines if it's an Insert (new to local view) or Update (existing).
3.  **Client Responsibility**: The client receives the stream and re-sorts/trims the collection locally.

**Trade-off**: This might send some "historical" updates that are outside the user's current view (e.g., an edit to a message from last year). However, in a chat scenario, this is often desirable (consistency) or acceptable given the massive simplification of server logic.

### 5.4 Strict-mode Error Shape
When a query lacks a required index in strict mode, return a structured error:
```json
{
  "code": "MISSING_INDEX",
  "message": "Query requires index on fields: data.status asc, data.views desc",
  "suggestedIndex": {
    "collection": "documents",
    "fields": [
      {"path": "data.status", "direction": "asc"},
      {"path": "data.views", "direction": "desc"}
    ]
  }
}
```
Why: helps CLI/clients surface actionable remediation; How: planner detects missing index and attaches the minimal covering index definition.
