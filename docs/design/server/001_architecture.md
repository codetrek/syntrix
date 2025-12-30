# Syntrix Architecture Discussion

**Date:** December 12, 2025
**Topic:** Initial Architecture & Tech Stack Decisions

## 1. Project Goal

Build a Firestore-like database system with the following core characteristics:

- **Document-Collection Model**: Hierarchical structure (Collection → Document → Subcollection).
- **Realtime**: Push updates to clients.
- **Offline-first**: Robust local caching and synchronization.
- **Consistency**: Eventual consistency for cross-client sync; Strong consistency (CAS) for single-document operations.

## 2. High-Level Architecture

### 2.1 System Overview

```text
┌─────────────────────────────────────────────────────────────────┐
│                         Client SDKs                             │
│              (Go / TypeScript / REST API)                       │
└─────────────────────────────────────────────────────────────────┘
                                │
                                ▼
┌─────────────────────────────────────────────────────────────────┐
│                       API Gateway Layer                         │
│         (gRPC / HTTP / WebSocket for realtime)                  │
└─────────────────────────────────────────────────────────────────┘
                                │
                                ▼
┌─────────────────────────────────────────────────────────────────┐
│                        Query Engine                             │
│    (Query Parser → Planner → Executor → Result Builder)         │
└─────────────────────────────────────────────────────────────────┘
                                │
                                ▼
┌─────────────────────────────────────────────────────────────────┐
│                       Storage Backend                           │
│                           MongoDB                               │
│         (Change Streams / Transactions / Indexes)               │
└─────────────────────────────────────────────────────────────────┘
```

### 2.2 Component Interaction (Splittable Monolith)

```text
┌───────────────────────────────────────────┐
│              1. Client SDKs               │
│  (RxDB / REST Clients / Go SDK / TS SDK)  │
└──────────────────┬────────────────────────┘
  v (HTTP/gRPC/WS/SSE)
                   v
┌───────────────────────────────────────────────┐
│ 2. Gateway (--gateway)                        │
│ (Auth / REST / Replication / WS & SSE realtime) │
└──────────┬────────────────────────────────────┘
           │
           v
┌─────────────────────────────────┐     ┌──────────────────────────────┐     ┌─────────────────────────────────┐
│    3. Query Engine (--query)    │     │ 4. Change Stream Processor   │     │ 5. Trigger Delivery Service     │
│ (CRUD / Parser / Aggregator)    │ <-- │  (Listen / Filter / Route)   │ --> │ (Async Webhooks / DLQ / Retry)  │
└──────────────┬──────────────────┘     └──────────────────────────────┘     └─────────────────────────────────┘
               |                                   ^
               v                                   |
   ┌───────────────────────────────────┐           |
   │              6. MongoDB           │-----------┘
   │    (Change Streams / Indexes)     │
   └───────────────────────────────────┘
```

### 2.3 Component Responsibilities

1. **Client SDKs**: Client SDKs to act with server.
2. **Gateway (API + Realtime)**: Single entry for REST/gRPC/WS/SSE.
   - **Auth**: Authentication & Authorization.
   - **REST API**: Standard CRUD endpoints.
   - **Replication**: Handles `pull` and `push` over HTTP (formerly "Replication Worker").
   - **Realtime**: Connection mgmt (auth/heartbeats), subscription tracking, initial snapshot via Query Engine, push events from CSP to clients over WS/SSE.
3. **Query Engine**: The Unified Data Access Layer.
    - **CRUD**: Handles all Create/Read/Update/Delete operations.
    - **Query**: Parses DSL, executes aggregations.
    - **Storage Abstraction**: Hides MongoDB details from upper layers.
4. **Change Stream Processor (CSP)**: The "Heart" of the realtime system.
    - **Listener**: Consumes the raw MongoDB Change Stream.
    - **Transformer**: Converts BSON events to internal `ChangeEvent` structs.
    - **Router**: Matches events against active subscriptions (via Query Engine logic) and routes them to the Query Engine nodes.
5. **Trigger Delivery Service**: Dedicated module for triggers and outbound webhooks.
    - **Async Delivery**: Consumes matched trigger events fanned out by CSP into queues, signs, and sends webhooks.
    - **Reliability**: Retries with backoff, DLQ on exhaustion, idempotency keys to avoid duplicates.
    - **Isolation**: Protects API/CSP latency from slow or failing external endpoints; enforces per-tenant rate limits and timeouts.
6. **MongoDB**: Storage backend.

## Identity Design Docs

- [Identity architecture](identity/001.architecture.md)
- [Authentication](identity/002.authentication.md)
- [Authorization rules](identity/003.authorization.md)

### 2.4 Component Dependency Analysis

**Why Replication Worker was merged into API Gateway?**

- **Protocol Alignment**: RxDB replication uses standard HTTP POST requests (`/pull`, `/push`), just like the REST API.
- **Simplification**: Merging them reduces the number of deployable units and simplifies the architecture. The "Replication Worker" is now just a logical module within the API Gateway.

**Why Query Engine handles CRUD?**

- **Unified Access**: To ensure consistency and apply validation rules uniformly, all data access (Read and Write) should go through a single layer.
- **Naming**: We retain the name "Query Engine" for now, but it effectively functions as the **Data Service** or **Storage Layer**.

## 3. Key Decisions

### 3.1 Storage Layer: MongoDB

We chose MongoDB as the underlying storage engine.

**Reasons:**

- **Native Document Model**: JSON-like structure fits perfectly.
- **Maturity**: Production-ready reliability.
- **Change Streams**: Native support for realtime subscriptions.
- **Atomic Operations**: Single-document atomicity with optimistic locking (CAS).
- **Scalability**: Sharding support for future growth.

**Data Modeling Strategy:**
We lean towards a **Single Collection + Path Fields** approach.

```javascript
// MongoDB Collection: "documents"
{
  "_id": "users/alice/posts/post1",           // Full path as ID
  "_path": "users/alice/posts/post1",         // Redundant for querying
  "_parent": "users/alice/posts",             // Parent path
  "collection": "posts",                     // Collection name
  "_updatedAt": ISODate("..."),
  // User data...
  "title": "Hello World"
}
```

*Pros*: Simpler Change Stream management, easier path-based queries.

### 3.2 Client SDK: RxDB

We chose RxDB as the primary client-side solution.

#### **Reasons:**

- **Realtime & Reactive**: Built on RxJS Observables.
- **Offline-first**: Best-in-class local storage and sync.
- **Replication Protocol**: Well-defined protocol for custom backends.
- **Ecosystem**: Supports React, Vue, Angular, etc.

#### **Integration Strategy:**

1. Develop **`@syntrix/rxdb-plugin`**: Implements RxDB's replication protocol to talk to Syntrix.
2. (Future) Develop **`@syntrix/client`**: A lightweight SDK for simple use cases without the full RxDB weight.

### 3.3 rest API

We will provide a standard REST API for server-side integration and simple clients.

- **CRUD**: Standard HTTP methods (`GET`, `POST`, `PUT`, `DELETE`, `PATCH`) mapped to document paths under `/api/v1/...`.
- **Realtime**: Explicit endpoints `/realtime/ws` (WebSocket) and `/realtime/sse` (SSE) for realtime subscriptions.

### 3.4 Component Architecture

We will adopt a **Splittable Monolith** strategy.

- **Monorepo**: Single repository for all components.
- **Multiple Entry Points**: Separate `main.go` for each component in `cmd/`.
- **Shared Kernel**: Core logic in `internal/` and `pkg/`.

**Components:**

1. **Gateway (`syntrix --gateway`)**: Unified REST/gRPC/WS/SSE entry; handles auth, CRUD routing, replication (`/replication/v1/pull`, `/replication/v1/push`), and realtime delivery at `/realtime/ws` and `/realtime/sse`.
2. **Query Engine (`cmd/syntrix --query`)**: Parses queries, executes aggregations, and interacts with storage.
3. **Standalone (`cmd/syntrix --all`)**: All-in-one binary for development and simple deployments.

## 2.5 Gateway Merge Details (Latest Design)

- Single port for REST/Replication and Realtime.
- Routes: REST/Replication keep `/api/v1/...`; Realtime uses `/realtime/ws` (WebSocket) and `/realtime/sse` (SSE).
- Code layout under `internal/api/`:
  - `rest/`: existing REST + replication handlers/routers/middleware.
  - `realtime/`: connection/auth/heartbeat/subscription/push/limits.
  - `server.go`: compose mux and HTTP server for both REST and realtime.
- Config: use the existing config module with a single unified gateway node (no legacy realtime keys). Fields include `port`, `tls`, `auth`, `limits`, `timeouts`, `cors`, `gzip`, `realtime` (ws/sse/perMessageDeflate), and optional `replication` limits.
- Manager: single Gateway service; no separate realtime service.
- Lifecycle: single HTTP server start; graceful shutdown closes WS/SSE and completes REST in-flight.

## Action Plan (Step-by-step, design only)

1) Config unify: define unified gateway config schema (single node, no legacy fields/flags/env); document new keys.
2) Directory structure: ensure `internal/api/rest` and `internal/api/realtime` exist under `internal/api`; migrate existing code accordingly.
3) Server assembly: in `internal/api/server.go`, register REST/replication routes and `/realtime/ws|sse` on one mux/server.
4) Manager wiring: register only the unified Gateway service in `internal/services`; remove realtime-specific service entries/tests.
5) Command entry: adjust `cmd/syntrix/main.go` to start the unified gateway with the new config; drop old realtime flags.
6) Docs: update config examples and operational docs to reflect single gateway and new realtime paths.
7) Testing (to add during implementation): single-port smoke (REST + WS/SSE), auth parity REST/WS/SSE, replication behavior, realtime limits/heartbeats, config parsing (new fields only), graceful shutdown.

## 4 User-Facing Document (Business Layer Type)

### 4.1 Document Structure

This is the business layer Document type, visible to the API.

```go
type Document map[string]interface{}
```

```json
{
  "id": "alice",               // Required; immutable once written; allowed charset: [A-Za-z0-9_.-]
  "collection": "chats/chatroom-1/members", // Shadow field, server-written; client input ignored
  "version": 0,                // Shadow field, server-written; client input ignored
  "updatedAt": 1700000000000,  // Shadow field, server-written; client input ignored
  "createdAt": 1700000000000,  // Shadow field, server-written; client input ignored
  "deleted": false,
  /* other user fields */
}
```

#### **Rules & protections**

- `id` is required and immutable after creation; server enforces charset `[A-Za-z0-9_.-]` and rejects mutations on update/patch.
- Shadow fields (`collection`, `version`, `updatedAt`, `createdAt`) are server-owned; client-supplied values are ignored/overwritten.

### 4.3 Soft Delete Mechanism

When a document is deleted via the API or Storage interface, it is **soft deleted** instead of being immediately removed from the database.

1. **Marked as Deleted**: The `deleted` field is set to `true`.
2. **Data Cleared**: The `data` field is cleared (set to empty) to save space and ensure privacy.
3. **Expiration**: A `sys_expires_at` field is set based on the configured retention period (default 30 days). MongoDB's TTL index will automatically remove the document after this time.
4. **Visibility**: Soft-deleted documents are excluded from `Get` and `Query` operations by default.
5. **Re-creation**: If a document is created with the same ID as a soft-deleted one, the soft-deleted document is overwritten (revived).
6. **Watch Events**:
    - Soft deletion emits a `delete` event.
    - Reviving a soft-deleted document emits a `create` event.

- `version` in MongoDocument is incremented automatically per write and not client-settable.
- `createdAt` is set once on create; `updatedAt` updates on every write.

## 5. Public API Interface

### 5.1 REST API

- `GET /api/v1/{collectionPath}/{id}`: Get a document.
- `POST /api/v1/{collectionPath}`: Create a document (auto-generated ID).
- `PUT /api/v1/{collectionPath}/{id}`: Create or Replace a document.
- `PATCH /api/v1/{collectionPath}/{id}`: Update specific fields.
- `DELETE /api/v1/{collectionPath}/{id}`: Delete a document.
- `POST /api/v1/query`: Execute a structured query (JSON body).

### 5.2 Realtime API (WebSocket)

- **Endpoint**: `/realtime/ws` (WebSocket), `/realtime/sse` (SSE)
- **Protocol**: JSON-based messages.
  - `{"type": "auth", "token": "..."}`
  - `{"type": "subscribe", "id": "sub1", "query": {...}}`
  - `{"type": "unsubscribe", "id": "sub1"}`
  - `{"type": "event", "subId": "sub1", "delta": {...}}` (Server -> Client)

### 5.3 RxDB Replication API

- `POST /replication/v1/pull`: Fetch changes since checkpoint.
- `POST /replication/v1/push`: Push local changes.
- *Note: The stream part of replication uses the standard `/api/v1/realtime` WebSocket endpoint.*

### 5.4 Trigger API

- `POST /api/v1/trigger/get`: batch document reads by concrete paths.

- `POST /api/v1/trigger/query`: single query (same shape as public query API).
- `POST /api/v1/trigger/write`: one or more write ops in a single request.

## 6. Usage Examples

### 6.1 Chat Message (REST)

**Create a message:**

```bash
POST /api/v1/rooms/room-123/messages
{
  "senderId": "user-alice",
  "text": "Hello everyone!",
  "timestamp": 1678888888000
}
```

**Query recent messages:**

```bash
POST /api/v1/query
{
  "collection": "rooms/room-123/messages",
  "filters": [
    {"field": "timestamp", "op": ">", "value": 1678880000000}
  ],
  "orderBy": [{"field": "timestamp", "direction": "desc"}],
  "limit": 50
}
```

### 6.2 Realtime Subscription (WebSocket)

**Client sends:**

```json
{
  "type": "subscribe",
  "id": "sub-room-123",
  "query": {
    "collection": "rooms/room-123/messages",
    "filters": [{"field": "timestamp", "op": ">", "value": 1678888888000}]
  }
}
```

**Server responds (Snapshot):**

```json
{
  "type": "snapshot",
  "subId": "sub-room-123",
  "docs": [ ... ]
}
```

**Server pushes (Delta):**

```json
{
  "type": "event",
  "subId": "sub-room-123",
  "delta": {
    "op": "insert",
    "doc": { "id": "msg-999", "text": "New message!", ... }
  }
}
```

## 7. Realtime Matching Strategy Analysis

### 7.1 The Challenge: "Live Query" Matching

The core problem is matching a high volume of **Document Change Events** (from MongoDB Change Streams) to thousands of active **User Subscriptions** (Queries).

**Key Difficulties:**

1. **Matching Efficiency**: With 100k+ concurrent subscriptions, we cannot iterate through all of them for every database write.
2. **"Enter/Leave" View Problem**:
   - **Enter**: A document is updated and now matches a query (e.g., `status` changed to "published").
   - **Leave**: A document is updated and no longer matches.
   - **Move**: A document's sort order changes.
3. **The "Limit" Complexity**: If a user subscribes to `Limit: 50`, and a new document is inserted at position 1, the document at position 50 falls out of the view. The server needs to know *which* document fell out to notify the client, which implies maintaining the current result set state for every subscription.

### 5.2 Alternatives Considered

#### Option A: Server-Side Sliding Window (Stateful)

The server maintains a "Boundary Value" (e.g., the timestamp of the 50th message) for every subscription.

- **Logic**: Only push updates if `NewDoc.Timestamp > Boundary`.
- **Pros**: Saves bandwidth by not sending irrelevant historical updates.
- **Cons**:
  - **High Complexity**: Handling deletions is hard (if #49 is deleted, we need to fetch #51 from DB to maintain the window).
  - **Stateful**: Increases memory usage per connection.

#### Option B: Push-All-Matching (Stateless / Client-Side Limit)

The server ignores `Limit` and `OrderBy` during the streaming phase. It only evaluates `Filters`.

- **Logic**: If `Doc` matches `Filters`, push it.
- **Pros**:
  - **Stateless**: Server doesn't need to know the current state of the client's view.
  - **Simple**: Matching is reduced to a boolean check (or hash lookup for equality).
  - **Leverages Client**: Smart clients like RxDB are designed to ingest a stream of updates and maintain the sorted/limited view locally.

### 7.3 Decision: Push-All-Matching

We chose **Option B** for the initial architecture.

**Implementation Details:**

1. **Initial Snapshot**: The server executes the full query (Filter + Sort + Limit) and returns the initial result set to populate the client.
2. **Streaming Updates**:
   - The server **ignores** `Limit` and `OrderBy`.
   - The server pushes **all** document changes that match the `Filters`.
   - **Upsert Semantics**: We treat updates as "Upserts". If a document matches the filter, we push it. The client determines if it's an Insert (new to local view) or Update (existing).
3. **Client Responsibility**: The client receives the stream and re-sorts/trims the collection locally.

**Trade-off**: This might send some "historical" updates that are outside the user's current view (e.g., an edit to a message from last year). However, in a chat scenario, this is often desirable (consistency) or acceptable given the massive simplification of server logic.

### 7.4 Strict-mode Error Shape

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
