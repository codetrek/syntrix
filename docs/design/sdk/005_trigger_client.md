# TriggerClient Design

**Date:** December 22, 2025
**Status:** Planned
**Related:** [001_sdk_architecture.md](001_sdk_architecture.md), [003_authentication.md](003_authentication.md)

## Scope
HTTP client for trigger workers using `/v1/trigger/...` endpoints with privileged capabilities (e.g., atomic batch writes). Implements `StorageClient` but with trigger-specific semantics.

## Responsibilities
- CRUD semantics over trigger endpoints where applicable.
- Batch writes via `batch(writes: WriteOp[])` mapped to `/v1/trigger/write`.
- Provide reference API entry points `collection(path)` and `doc(path)` consistent with `StorageClient` contract.
- Accept `preIssuedToken` (short-lived, scoped) from trigger payload; no automatic refresh.

## Non-Responsibilities
- Refreshing tokens (payload token is authoritative; no refresh hook).
- Application-level retries beyond basic network retry (caller may add).
- Managing replication; trigger context is typically server-side stateless.

## API Surface (current)
- `constructor(baseURL: string, token: string)`
- `collection<T>(path): CollectionReference<T>`
- `doc<T>(path): DocumentReference<T>`
- `batch(writes: WriteOp[]): Promise<void>`
- `get(path): Promise<T | null>` (via `/v1/trigger/get`)
- `create(collectionPath, data, id): Promise<T>` (id required)
- `update(path, data): Promise<T>`
- `replace(path, data): Promise<T>`
- `delete(path): Promise<void>`
- `query(query: Query): Promise<T[]>` (via `/v1/trigger/query`)

## Behavior Notes
- `create` requires caller-provided `id`; server does not generate one in trigger mode.
- `get` uses trigger-specific get endpoint and returns first document or null; tolerant to empty responses.
- `batch` is the primary write path; other operations are convenience wrappers around it.
- Uses JSON payloads; expects `preIssuedToken` auth header.

## Usage Examples
### Trigger handler flow
```typescript
// Initialized from webhook payload
const handler = new TriggerHandler(payload, process.env.SYNTRIX_URL);
const client = handler.syntrix; // TriggerClient

// Standard operation
await client.doc(payload.collection).update({ status: 'processed' });

// Atomic batch write
await client.batch([
	{ type: 'update', path: 'users/alice', data: { credits: 10 } },
	{ type: 'create', path: 'logs/123', data: { event: 'credit_added' } }
]);
```

## Auth
- Uses `preIssuedToken` from trigger payload; no refresh. If missing/invalid, fail fast.
- Auth handling is simpler than SyntrixClient; still reuses header injection patterns but without refresh logic.

## Testing Plan
- `create` without id rejects; with id sends correct path.
- `batch` posts writes as-is to `/v1/trigger/write`.
- `get` returns null on empty documents array.
- `query` posts to `/v1/trigger/query` with given `Query` shape.
- Auth: ensures header is set; missing token yields error.
