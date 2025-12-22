# TypeScript Client SDK Architecture

**Date:** December 21, 2025
**Status:** Implemented

## 1. Overview

The Syntrix TypeScript SDK is designed with a **"Semantic Separation, Shared Abstraction"** philosophy. It provides two distinct clients to address the fundamentally different requirements of external applications and internal trigger workers, while sharing a common fluent API for developer experience.

## 2. Core Design Principles

### 2.1 Semantic Separation
We explicitly reject the "One Client Fits All" approach. The lifecycle, authentication, and capabilities of an external app differ significantly from a server-side trigger worker.

*   **`SyntrixClient` (Standard)**:
    *   **Target**: External applications (Web, Mobile, Backend).
    *   **Auth**: Long-lived tokens or User tokens.
    *   **Transport**: Standard REST API (`/v1/...`).
    *   **Semantics**: Standard HTTP behavior (e.g., 404 returns null).

*   **`TriggerClient` (Trigger)**:
    *   **Target**: Internal Trigger Workers (Serverless/Container).
    *   **Auth**: Ephemeral `preIssuedToken` (Strictly scoped to the trigger event).
    *   **Transport**: Internal Trigger RPC (`/v1/trigger/...`).
    *   **Capabilities**: Privileged operations like **Atomic Batch Writes** (`batch()`).

### 2.2 Interface-Based Polymorphism
To decouple the high-level API from the underlying transport (REST vs RPC), we define a core contract:

```typescript
/** @internal */
export interface StorageClient {
    get<T>(path: string): Promise<T | null>;
    create<T>(collection: string, data: any, id?: string): Promise<T>;
    update<T>(path: string, data: any): Promise<T>;
    replace<T>(path: string, data: any): Promise<T>;
    delete(path: string): Promise<void>;
    query<T>(query: Query): Promise<T[]>;
}
```

Both `SyntrixClient` and `TriggerClient` implement this interface. This allows the upper-level "Reference" API to work identically regardless of which client is being used.

### 2.3 Developer Experience (DX) First - Fluent API
We prioritize readability and ease of use by exposing a **Reference-based** API (inspired by Firestore) rather than raw HTTP methods.

*   **CollectionReference**: `client.collection('users')`
*   **DocumentReference**: `client.doc('users/alice')`
*   **QueryBuilder**: `client.collection('posts').where('status', '==', 'published').orderBy('date')`

### 2.4 Internal Encapsulation
Implementation details that users shouldn't depend on are hidden in the `src/internal` directory and marked with `/** @internal */`. This keeps the public API surface clean and prevents leaky abstractions.

## 3. Architecture

```mermaid
graph TD
    UserApp[External App] --> SyntrixClient
    TriggerWorker[Trigger Worker] --> TriggerHandler --> TriggerClient

    subgraph Public API
        SyntrixClient
        TriggerClient
        Ref[Reference API (doc, collection)]
    end

    subgraph Internal Implementation
        SyntrixClient -- implements --> StorageClient
        TriggerClient -- implements --> StorageClient
        Ref -- depends on --> StorageClient
    end

    SyntrixClient -- HTTP REST --> Server[/v1/...]
    TriggerClient -- Trigger RPC --> Server[/v1/trigger/...]
```

## 4. Implementation Details

### 4.1 Directory Structure
```text
pkg/syntrix-client-ts/src/
├── api/                # Reference layer
├── clients/            # SyntrixClient, TriggerClient
├── internal/           # 🔒 StorageClient contract & helpers
├── replication/        # RxDB replication adapters (planned)
├── trigger/            # TriggerHandler helper
├── types.ts            # Shared types
└── index.ts            # Public exports
```

### 4.2 TriggerClient Specifics
The `TriggerClient` includes exclusive methods not available in the standard client, such as `batch()`, which maps directly to the backend's transactional write capability for triggers.

```typescript
// Exclusive to TriggerClient
async batch(writes: WriteOp[]): Promise<void> {
    await this.client.post('/v1/trigger/write', { writes });
}
```

## 5. Usage Examples

### Standard Client
```typescript
const client = new SyntrixClient('https://api.syntrix.io', 'user-token');

// Fluent Read
const user = await client.doc('users/alice').get();

// Fluent Write
await client.collection('users/alice/posts').add({
    title: 'Hello World',
    createdAt: Date.now()
});
```

### Trigger Worker
```typescript
// Automatically initialized from webhook payload
const handler = new TriggerHandler(payload, process.env.SYNTRIX_URL);
const client = handler.syntrix; // Returns TriggerClient

// Standard operations work identically
await client.doc(payload.collection).update({ status: 'processed' });

// Advanced: Atomic Batch Write
await client.batch([
    { type: 'update', path: 'users/alice', data: { credits: 10 } },
    { type: 'create', path: 'logs/123', data: { event: 'credit_added' } }
]);
```

## 6. Replication Integration (RxDB)

### 6.1 Transport Contract
- **Pull**: `GET /v1/replication/pull?collection=...&checkpoint=...&limit=...` (checkpoint is a stringified int64; limit 0–1000).
- **Push**: `POST /v1/replication/push` with body `{ collection, changes: [{ doc }] }`; conflicts are returned as `conflicts` array.
- **Document shape (client-facing)**: responses expose logical `id` (path segment), `collection`, `data`, `version`, `updatedAt`, `createdAt`, optional `deleted`. The server’s internal hash ID is opaque and never returned. Clients send only `collection` + `id` + `data`; any client-sent `version/updatedAt/fullpath/parent/deleted` are ignored. Soft delete only via `action=delete` in changes.

### 6.2 SDK Adapter Shape (planned in `src/replication/`)
- Factory: `setupSyntrixReplication<T>({ collectionPath, baseURL, token, rxCollection, live?, pull?: { batchSize?, retryTime? }, push?: { retryTime? }, headers?, mapDocToPush?, mapPulledDoc? }) => RxReplicationState<T>`.
- `collectionPath` matches backend paths (do not rely on RxDB collection name).
- Pull handler maps `batchSize` to `limit`; uses returned `checkpoint` string.
- Push handler maps RxDB docs to `{ doc }` with only `collection`, `id`, `data`; server derives `fullpath/parent/version/updatedAt/deleted`. Soft delete is expressed as `action: "delete"`.
- Hooks `mapDocToPush` / `mapPulledDoc` let apps adapt schemas without leaking SDK internals.
- `live` enables continuous replication; stream/WebSocket can be added later.

### 6.2.1 Token Management
- Replication client must not mutate shared axios instances of `SyntrixClient`; introduce a lightweight token manager (per-instance) to avoid tokens being overwritten when both clients coexist.

### 6.3 Client-Side Storage Guidance
- **RxStorage Dexie** on IndexedDB for browser environments.
- Avoid over-fragmenting collections; prefer fewer shared collections with tenant/room fields.
- Keep batch sizes modest (e.g., pull limit 100–500; similar push chunking) to keep IndexedDB transactions fast.
- Only add necessary indexes; each index slows writes.
- Enable RxDB leader election so one tab handles writes/replication, others mirror state to reduce lock contention.
- Periodically cleanup deleted/expired docs to control storage growth and init time; consider selective caching for cold data or constrained devices.

### 6.4 Forward Compatibility & Live Stream
- **Checkpoint evolution**: current checkpoint is a stringified int64; design adapter to allow future upgrade to structured checkpoints (e.g., `{ updatedAt, id }`) without breaking callers.
- **Live mode**: Prefer existing `/v1/realtime` (WebSocket/SSE) for live replication feed; periodic pull remains a fallback.
