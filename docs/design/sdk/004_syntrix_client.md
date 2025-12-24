# SyntrixClient Design

**Date:** December 22, 2025
**Status:** Planned
**Related:** [001_sdk_architecture.md](001_sdk_architecture.md), [003_authentication.md](003_authentication.md)

## Scope
Public HTTP client for application usage (web/mobile/backend) over `/api/v1/...` REST. Implements `StorageClient` to power the reference API (CollectionReference/DocumentReference/QueryBuilder) and is reused by replication.

## Responsibilities
- CRUD and query over REST.
- Token injection via shared auth surface (003); retry once on 401/403 if refresh is provided.
- Surface 404 as `null` for `get()`; propagate other HTTP errors.
- Provide reference API entry points `collection(path)` and `doc(path)`.
- Remain side-effect free for replication: replication workers can share auth but own their Axios instance.

## Non-Responsibilities
- Managing refresh token storage (caller responsibility).
- Trigger-specific batch writes (handled by TriggerClient).
- UI/session management.

## API Surface (current)
- `constructor(baseURL: string, token: string | TokenProvider)` (to be aligned with 003 for tokenProvider/refresh hooks).
- `collection<T>(path): CollectionReference<T>`
- `doc<T>(path): DocumentReference<T>`
- `get(path): Promise<T | null>`
- `create(collectionPath, data, id?): Promise<T>`
- `update(path, data): Promise<T>`
- `replace(path, data): Promise<T>`
- `delete(path): Promise<void>`
- `query(query: Query): Promise<T[]>`

## Behavior Notes
- Base URL and auth headers are applied via Axios; content-type JSON.
- `get` treats 404 as `null`; other status codes propagate.
- `create` allows optional client-supplied `id`; payload merges if provided.
- All methods are promise-based; no retries beyond auth-refresh retry.

## Usage Examples
### Standard client
```typescript
const client = new SyntrixClient('https://api.syntrix.io', 'user-token');

// Fluent read
const user = await client.doc('users/alice').get();

// Fluent write
await client.collection('users/alice/posts').add({
	title: 'Hello World',
	createdAt: Date.now()
});
```

### Reference chaining
```typescript
const posts = await client
	.collection('posts')
	.where('status', '==', 'published')
	.orderBy('date', 'desc')
	.limit(10)
	.get();
```

## Error Handling
- Network errors propagate; caller may wrap with their retry/backoff.
- Auth errors: follow 003 rules (single refresh + retry, then bubble). No checkpoint mutation here.

## Testing Plan
- `get` returns null on 404; throws on other errors.
- `create` with/without id forwards payload correctly.
- Auth: 401 triggers refresh once and retries; failure bubbles.
- Query maps to POST `/api/v1/query` with provided `Query` shape.
- Reference API (`collection().doc().get/set/update/delete`) calls underlying methods correctly.
