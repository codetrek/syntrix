# TypeScript Client SDK Architecture

**Date:** December 21, 2025
**Status:** Architecture finalized; submodules pending implementation

**Related:** [003_authentication.md](003_authentication.md) defines the shared auth surface used by HTTP clients, replication, and realtime. Client specifics: [004_syntrix_client.md](004_syntrix_client.md), [005_trigger_client.md](005_trigger_client.md).

**Usage examples:** see [004_syntrix_client.md](004_syntrix_client.md#usage-examples) and [005_trigger_client.md](005_trigger_client.md#usage-examples).

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
## 5. Replication (overview)
High-level replication approach: reuse `StorageClient`-based transports for pull/push; RxDB handles local state; realtime events only trigger pulls while checkpoints are advanced by pull responses. Detailed replication design, auth interplay, and testing plan are in [002_replication_client.md](002_replication_client.md).

## 6. Primary Test Coverage (to implement)
- SyntrixClient: 401/403 triggers single refresh+retry; 404 returns null; create with/without id; query posts correct shape.
- TriggerClient: create without id rejects; batch forwards writes; trigger/get empty array returns null; missing token fails fast.
- Authentication layer: serialized refresh under concurrent 401s; hooks fire (auth error/retry/refreshed) without leaking tokens; realtime auth failure closes channel and resumes after new token.
- Replication (per 002): auth failures do not advance checkpoint; refresh then resume; realtime-triggered pull scheduling coalesces; outbox/pull concurrency doesn’t corrupt checkpoint.

More cases (error corners, perf, GC policies) will be added alongside implementation.
