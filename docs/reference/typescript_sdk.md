# TypeScript Client SDK Reference

The `@syntrix/client` package provides a type-safe interface for Syntrix. It includes `SyntrixClient` for external apps and `TriggerClient` for trigger workers.

## Installation

```bash
npm install @syntrix/client
# or
bun add @syntrix/client
```

## 1. SyntrixClient (Standard)

Use this client in external applications (Web, Mobile, Backend). Multi-database auth requires a database ID during login.

```typescript
import { SyntrixClient } from '@syntrix/client';

const client = new SyntrixClient('<URL_ENDPOINT>', {
  database: 'my-database',
});

await client.login('username', 'password', 'my-database');
```

### Methods

#### `doc<T>(path: string): DocumentReference<T>`

Creates a reference to a document.

#### `collection<T>(path: string): CollectionReference<T>`

Creates a reference to a collection.

## 2. TriggerClient (Internal)

Use only within Syntrix Trigger Workers. It requires the `preIssuedToken` from the webhook payload.

```typescript
import { TriggerHandler, WebhookPayload } from '@syntrix/client';

const payload = req.body as WebhookPayload;
const handler = new TriggerHandler(payload, process.env.SYNTRIX_API_URL);
const client = handler.syntrix; // TriggerClient
```

### Exclusive Methods

#### `batch(writes: WriteOp[]): Promise<void>`

Performs an atomic batch of write operations.

```typescript
await client.batch([
  { type: 'create', path: 'users/123', data: { name: 'Alice' } },
  { type: 'update', path: 'stats/daily', data: { count: 1 } },
]);
```

## 3. Fluent API (Shared)

Both clients return `DocumentReference` and `CollectionReference` objects with the same API.

### DocumentReference `<T>`

- **`get(): Promise<T | null>`** — fetch the document.
- **`set(data: T): Promise<T>`** — overwrite the document.
- **`update(data: Partial<T>): Promise<T>`** — partial update.
- **`delete(): Promise<void>`** — logically delete the document, retaining a
  tombstone and clearing its business data. See the
  [document deletion contract](api.md#delete-document); physical cleanup is a
  separate storage operation.
- **`collection(path: string): CollectionReference`** — sub-collection reference.

### CollectionReference `<T>`

- **`doc(id: string): DocumentReference<T>`** — reference by ID.
- **`add(data: T, id?: string): Promise<DocumentReference<T>>`** — create (auto-ID for standard client).
- **`get(): Promise<T[]>`** — list documents.
- **`where(field, op, value): QueryBuilder<T>`** — start a query.
- **`orderBy(field, direction): QueryBuilder<T>`** — sort.
- **`limit(n: number): QueryBuilder<T>`** — limit results.

## 4. Realtime (WS & SSE)

### WebSocket (default)

```typescript
const rt = client.realtime();
rt.on('onEvent', (evt) => console.log(evt));
await rt.connect(); // Auth sent via Bearer token; database enforced server-side

const sub = rt.subscribe({
  query: { collection: 'users' },
});
```

### Server-Sent Events (SSE)

```typescript
const sse = client.realtimeSSE();
await sse.connect(
  {
    onEvent: (evt) => console.log(evt),
    onSnapshot: (snap) => console.log(snap),
  },
  { collection: 'users' }
);
```

Notes:

- Authentication is sent via Authorization header (sourced from the SDK token provider); query-string tokens are rejected.

- WS auth failures trigger a single token refresh and retry; subsequent failures surface via `onError`.
