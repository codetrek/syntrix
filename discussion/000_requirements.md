# Syntrix Requirements Definition

**Date:** December 13, 2025
**Topic:** Core Requirements & Constraints

## 1. Core Scenarios
**Primary Goal:** General-purpose Realtime Document Database (Firestore-like).
**Reference Scenario (Stress Test):** Chat / Instant Messaging Applications.
- While designed for general use, we use Chat as the primary driver for performance requirements due to its demanding nature:
    - High frequency of small writes.
    - Realtime delivery is critical (<200ms).
    - Historical data retrieval (scroll back).
    - User presence / status updates.

## 2. Functional Requirements

### 2.1 Data Operations
- **CRUD**: Create, Read, Update, Delete documents.
- **Complex Queries**: Support filtering, sorting, and pagination (e.g., "Get last 50 messages in collection `rooms/room-X/messages` where timestamp < Y").
- **Batch Operations**: Bulk insert/update/delete (e.g., "Mark all messages as read").
- **Transactions**:
  - **Public API**: Multi-document transactions are **NOT** required. Single-document atomicity is sufficient.
  - **Internal Trigger API**: Multi-document transactions **ARE** required for the `/v1/trigger/write` endpoint to ensure atomic side-effects.

### 2.2 Realtime
- **Latency**: Target **< 200ms** for message delivery (server processing time).
  - *Strategy*: Rely on MongoDB Change Streams initially. Scale horizontally (sharding) to handle high load latency.
- **Push**: Server-initiated updates to connected clients.
- **Presence**: (Implicit) Ability to track online/offline status via connections.

## 3. Non-Functional Requirements

### 3.1 Consistency Model
- **Eventual Consistency**: Acceptable for cross-client synchronization.
- **Strong Consistency**: Required for single-document operations (CAS - Compare And Swap).

### 3.2 Availability & Reliability
- **Offline Support**: Clients must be able to read/write locally and sync when online (handled by RxDB).
- **Conflict Resolution**: Last-Write-Wins (LWW) or custom merge logic for offline sync.

### 3.3 Scalability
- **Concurrency**: Support high concurrency (10k+ active connections per node).
- **Data Volume**: Support **TB-level** data storage.
- **Horizontal Scaling**: Stateless API/Realtime layers; Sharded Storage layer.

### 3.4 Security
- **Authentication**: Standard Token-based auth (JWT).
- **Authorization**: **Data-Driven Rules** (similar to Firestore Security Rules).
  - Must support document-level access control based on data contents.
  - Example: "User X can read document if `resource.data.members` contains `request.auth.uid`".

## 4. Technology Stack Constraints
- **Language**: Go (Golang).
- **Storage**: MongoDB (Sharded cluster for TB scale).
- **Client SDK**: RxDB (Primary), REST API.
- **Deployment**:
  - **Dev**: Single machine (Docker Compose).
  - **Prod**: Kubernetes (K8s).

## 5. Implications on Design
1.  **Limited Transactions**:
    - Public API remains non-transactional to simplify scaling.
    - Internal Trigger API requires a transactional storage interface (e.g., MongoDB Transactions) to support atomic side-effects.
2.  **Chat Scenario**:
    - **Write Heavy**: Storage engine must handle high write throughput.
    - **Append Only**: Messages are mostly immutable (except edits/deletes).
    - **Time-series nature**: Queries are almost always sorted by time.
3.  **< 200ms Latency**:
    - Critical path (Receive -> Persist -> Push) must be optimized.
    - **Decision**: Use MongoDB Change Streams directly. Rely on MongoDB sharding for scaling write throughput and reducing change stream latency under load.
4.  **Data-Driven Auth**: Requires a rules engine in the API Gateway to evaluate permissions against document data.

## 6. Public API Interface (Draft)

### 6.1 REST API
- `GET /v1/{collectionPath}/{docID}`: Get a document.
- `POST /v1/{collectionPath}`: Create a document (auto-generated ID).
- `PUT /v1/{collectionPath}/{docID}`: Create or Replace a document.
- `PATCH /v1/{collectionPath}/{docID}`: Update specific fields.
- `DELETE /v1/{collectionPath}/{docID}`: Delete a document.
- `POST /v1/query`: Execute a structured query (JSON body).
- `POST /v1/batch`: Execute bulk operations.

### 6.2 Realtime API (WebSocket)
- **Endpoint**: `/v1/realtime`
- **Protocol**: JSON-based messages.
  - `{"type": "auth", "token": "..."}`
  - `{"type": "subscribe", "id": "sub1", "query": {...}}`
  - `{"type": "unsubscribe", "id": "sub1"}`
  - `{"type": "event", "subId": "sub1", "delta": {...}}` (Server -> Client)

### 6.3 RxDB Replication API
- `POST /v1/replication/pull`: Fetch changes since checkpoint.
- `POST /v1/replication/push`: Push local changes.
- *Note: The stream part of replication uses the standard `/v1/realtime` WebSocket endpoint.*

## 7. Usage Examples

### 7.1 Chat Message (REST)
**Create a message:**
```bash
POST /v1/rooms/room-123/messages
{
  "senderId": "user-alice",
  "text": "Hello everyone!",
  "timestamp": 1678888888000
}
```

**Query recent messages:**
```bash
POST /v1/query
{
  "collection": "rooms/room-123/messages",
  "filters": [
    {"field": "timestamp", "op": ">", "value": 1678880000000}
  ],
  "orderBy": [{"field": "timestamp", "direction": "desc"}],
  "limit": 50
}
```

### 7.2 Realtime Subscription (WebSocket)
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
