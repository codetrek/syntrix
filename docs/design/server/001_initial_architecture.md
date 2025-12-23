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
2.  **Gateway (API + Realtime)**: Single entry for REST/gRPC/WS/SSE.
  *   **Auth**: Authentication & Authorization.
  *   **REST API**: Standard CRUD endpoints.
  *   **Replication**: Handles `pull` and `push` over HTTP (formerly "Replication Worker").
  *   **Realtime**: Connection mgmt (auth/heartbeats), subscription tracking, initial snapshot via Query Engine, push events from CSP to clients over WS/SSE.
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
  "collection": "posts",                     // Collection name
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

### 3.3 rest API
We will provide a standard REST API for server-side integration and simple clients.

- **CRUD**: Standard HTTP methods (`GET`, `POST`, `PUT`, `DELETE`, `PATCH`) mapped to document paths under `/v1/...`.
- **Realtime**: Explicit endpoints `/realtime/ws` (WebSocket) and `/realtime/sse` (SSE) for realtime subscriptions.

### 3.4 Component Architecture
We will adopt a **Splittable Monolith** strategy.
- **Monorepo**: Single repository for all components.
- **Multiple Entry Points**: Separate `main.go` for each component in `cmd/`.
- **Shared Kernel**: Core logic in `internal/` and `pkg/`.

**Components:**
1. **Gateway (`syntrix --gateway`)**: Unified REST/gRPC/WS/SSE entry; handles auth, CRUD routing, replication (`/v1/replication/pull`, `/v1/replication/push`), and realtime delivery at `/realtime/ws` and `/realtime/sse`.
2. **Query Engine (`cmd/syntrix --query`)**: Parses queries, executes aggregations, and interacts with storage.
3. **Standalone (`cmd/syntrix --all`)**: All-in-one binary for development and simple deployments.

## 2.5 Gateway Merge Details (Latest Design)
- Single port for REST/Replication and Realtime.
- Routes: REST/Replication keep `/v1/...`; Realtime uses `/realtime/ws` (WebSocket) and `/realtime/sse` (SSE).
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
  - `WS /realtime/ws`: Realtime change notifications via WebSocket.
  - `GET /realtime/sse`: Realtime change notifications via SSE.
- Implement rest API endpoints:
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
