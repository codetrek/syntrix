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

```
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

```
┌──────────────────────────────────────────────┐
│              1. Client SDKs                  │
│  (RxDB / REST Clients / Go SDK / TS SDK)     │
└───────┬───────────────────────────────────┬──┘
        v (HTTP/gRPC)                       v (WebSocket)
┌─────────────────────────────────┐    ┌─────────────────────────────────┐
│      2. API Gateway (--api)     │    │3. Realtime Gateway (--realtime) │
│  (Auth / REST / RxDB Sync)      │    │ (Conn Mgr / Push Logic)         │
└──────────┬──────────────────────┘    └─────────────────────────────────┘
           │                                             ^
           v            ┌────────────────────────────────┘
┌─────────────────────────────────┐     ┌──────────────────────────────┐     ┌─────────────────────────────────┐
│    4. Query Engine (--query)    │ <-- │ 5. Change Stream Processor   │ --> │ 6. Trigger Delivery Service     │
│ (CRUD / Parser / Aggregator)    │     │ (Listen / Filter / Route)    │     │ (Async Webhooks / DLQ / Retry)  │
└──────────────┬──────────────────┘     └──────────┬───────────────────┘     └─────────────────────────────────┘
               └────────────┬──────────────────────┘
                            v
         ┌───────────────────────────────────┐
         │              7. MongoDB           │
         │    (Change Streams / Indexes)     │
         └───────────────────────────────────┘
```

### 2.3 Component Responsibilities
1. **Client SDKs**: Client SDKs to act with server.
2.  **API Gateway**: Entry point for REST/gRPC.
    *   **Auth**: Authentication & Authorization.
    *   **REST API**: Standard CRUD endpoints.
    *   **RxDB Sync**: Handles `pull` and `push` replication requests (formerly "Replication Worker").
3.  **Realtime Gateway**: Manages long-lived WebSocket connections.
    *   **Connection Mgmt**: Auth, Heartbeats.
    *   **Subscription Mgmt**: Tracks who watches what.
    *   **Push Logic**: Receives events from CSP and pushes Deltas to clients.
    *   **Snapshot**: Calls Query Engine for initial state.
4.  **Query Engine**: The Unified Data Access Layer.
    *   **CRUD**: Handles all Create/Read/Update/Delete operations.
    *   **Query**: Parses DSL, executes aggregations.
    *   **Storage Abstraction**: Hides MongoDB details from upper layers.
5.  **Change Stream Processor (CSP)**: The "Heart" of the realtime system.
    *   **Listener**: Consumes the raw MongoDB Change Stream.
    *   **Transformer**: Converts BSON events to internal `ChangeEvent` structs.
    *   **Router**: Matches events against active subscriptions (via Query Engine logic) and routes them to the Query Engine nodes.
6.  **Trigger Delivery Service**: Dedicated module for triggers and outbound webhooks.
    *   **Async Delivery**: Consumes matched trigger events fanned out by CSP into queues, signs, and sends webhooks.
    *   **Reliability**: Retries with backoff, DLQ on exhaustion, idempotency keys to avoid duplicates.
    *   **Isolation**: Protects API/CSP latency from slow or failing external endpoints; enforces per-tenant rate limits and timeouts.
7.  **MongoDB**: Storage backend.

### 2.4 Component Dependency Analysis

**Why Replication Worker was merged into API Gateway?**
*   **Protocol Alignment**: RxDB replication uses standard HTTP POST requests (`/pull`, `/push`), just like the REST API.
*   **Simplification**: Merging them reduces the number of deployable units and simplifies the architecture. The "Replication Worker" is now just a logical module within the API Gateway.

**Why Query Engine handles CRUD?**
*   **Unified Access**: To ensure consistency and apply validation rules uniformly, all data access (Read and Write) should go through a single layer.
*   **Naming**: We retain the name "Query Engine" for now, but it effectively functions as the **Data Service** or **Storage Layer**.

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
  "_collection": "posts",                     // Collection name
  "_updatedAt": ISODate("..."),
  // User data...
  "title": "Hello World"
}
```
*Pros*: Simpler Change Stream management, easier path-based queries.

### 3.2 Client SDK: RxDB
We chose RxDB as the primary client-side solution.

**Reasons:**
- **Realtime & Reactive**: Built on RxJS Observables.
- **Offline-first**: Best-in-class local storage and sync.
- **Replication Protocol**: Well-defined protocol for custom backends.
- **Ecosystem**: Supports React, Vue, Angular, etc.

**Integration Strategy:**
1. Develop **`@syntrix/rxdb-plugin`**: Implements RxDB's replication protocol to talk to Syntrix.
2. (Future) Develop **`@syntrix/client`**: A lightweight SDK for simple use cases without the full RxDB weight.

### 3.3 RESTful API
We will provide a standard REST API for server-side integration and simple clients.

- **CRUD**: Standard HTTP methods (`GET`, `POST`, `PUT`, `DELETE`, `PATCH`) mapped to document paths.
- **Realtime**: Use WebSocket (`/v1/realtime`) for all realtime subscriptions (see Realtime Protocol).

### 3.4 Component Architecture
We will adopt a **Splittable Monolith** strategy.
- **Monorepo**: Single repository for all components.
- **Multiple Entry Points**: Separate `main.go` for each component in `cmd/`.
- **Shared Kernel**: Core logic in `internal/` and `pkg/`.

**Components:**
1. **API Gateway (`syntrix --api`)**: Handles REST/gRPC requests, auth, RxDB replication (`/replication/pull`, `/replication/push`), and routing.
2. **Realtime Gateway (`cmd/syntrix --realtime`)**: Manages WebSocket connections and pushes updates.
3. **Query Engine (`cmd/syntrix --query`)**: Parses queries, executes aggregations, and interacts with storage.
4. **Standalone (`cmd/syntrix --all`)**: All-in-one binary for development and simple deployments.

## 4. Implementation Roadmap

### Phase 1: Core Storage & Interfaces (Week 1)
- Define `StorageBackend` interface in Go.
- Implement MongoDB backend (CRUD operations).
- Implement Path Resolver (`users/123/posts/456` parsing).
- Setup project structure with multiple `cmd/` entries.

### Phase 2: API Layer (Week 2)
- Implement RxDB Replication Protocol endpoints:
  - `POST /v1/replication/pull`: Fetch changes since checkpoint.
  - `POST /v1/replication/push`: Write local changes to server.
  - `WS /v1/realtime`: Realtime change notifications via MongoDB Change Streams.
- Implement RESTful API endpoints:
  - `GET /v1/{path}`: Get document or list collection.
  - `POST /v1/{collection}`: Create document.
  - `PATCH /v1/{path}`: Update document.
  - `DELETE /v1/{path}`: Delete document.

### Phase 3: Query Engine (Week 3)
- Implement Firestore-style query DSL.
- Translate queries to MongoDB Aggregation Pipelines.
- Support indexes and sorting.

### Phase 4: Polish & SDK (Week 4)
- Finalize `@syntrix/rxdb-plugin`.
- End-to-end testing with a sample Todo app.
- Security rules and validation.

## 5. Next Steps
1. Initialize Go module structure.
2. Define the `StorageBackend` interface.
3. Set up a local MongoDB for development.

## 6. Document Shapes (Mongo Storage vs User-Facing)

### 6.1 MongoDocument (persisted in MongoDB)
```json
{
  "_id": "<blake3(fullpath)>",          // 128-bit BLAKE3 of fullpath, binary
  "fullpath": "users/alice/posts/post1", // Full path, immutable
  "collection": "users/alice/posts",     // Redundant for querying, immutable
  "parent": "users/alice",               // Redundant for querying, immutable
  "version": 0,                           // Auto-increment per update, client cannot set
  "doc": { /* user Document */ },         // User payload (see 6.2)
  "createdAt": "ISODate(...)" ,          // Set on create only
  "updatedAt": "ISODate(...)"            // Updated on every write
}
```

### 6.2 User-Facing Document (API shape)
```json
{
  "id": "post1",               // Required; immutable once written; allowed charset: [A-Za-z0-9_.-]
  "_collection": "posts",      // Shadow field, server-written; client input ignored
  "_version": 0,                // Shadow field, server-written; client input ignored
  "_updatedAt": 1700000000000,  // Shadow field, server-written; client input ignored
  "_createdAt": 1700000000000,  // Shadow field, server-written; client input ignored
  /* other user fields */
}
```

**Rules & protections**
- `id` is required and immutable after creation; server enforces charset `[A-Za-z0-9_.-]` and rejects mutations on update/patch.
- Shadow fields (`_collection`, `_version`, `_updatedAt`, `_createdAt`) are server-owned; client-supplied values are ignored/overwritten.
- `version` in MongoDocument is incremented automatically per write and not client-settable.
- `createdAt` is set once on create; `updatedAt` updates on every write.
