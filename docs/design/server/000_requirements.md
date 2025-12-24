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
  - **Cross-document transactions are not supported**. Only single-document atomicity is guaranteed.
  - **Trigger workers must be idempotent/compensating** to handle partial successes when issuing multi-write batches.

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

1. **Limited Transactions**:
   - System supports only single-document atomicity to maximize throughput and simplify scaling.
   - Internal Trigger API relies on idempotent operations or higher-level compensation instead of cross-document ACID transactions.
2. **Chat Scenario**:
    - **Write Heavy**: Storage engine must handle high write throughput.
    - **Append Only**: Messages are mostly immutable (except edits/deletes).
    - **Time-series nature**: Queries are almost always sorted by time.
3. **< 200ms Latency**:
    - Critical path (Receive -> Persist -> Push) must be optimized.
    - **Decision**: Use MongoDB Change Streams directly. Rely on MongoDB sharding for scaling write throughput and reducing change stream latency under load.
4. **Data-Driven Auth**: Requires a rules engine in the API Gateway to evaluate permissions against document data.
