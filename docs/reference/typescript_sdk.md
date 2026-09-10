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
- **`delete(): Promise<void>`** — logically delete the document; see [tombstone semantics](api.md#delete-document).
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

The convenience API starts or reuses one authenticated WebSocket per client.
Each subscription owns its callbacks:

```typescript
const subscription = client.subscribe('users', {
  onReady: () => schedulePull(),
  onEvent: (event) => console.log(event),
  onSnapshot: (snapshot) => console.log(snapshot),
  onError: (error) => console.error(error),
});

subscription.unsubscribe();
```

`onReady` runs once after initial registration and again after each reconnect's
registration ACK. It signals registration success, not historical delivery or
snapshot completion. Applications can use it to schedule their own reconciliation.
Events and snapshots may arrive before the ACK. Unsubscribing removes only that
subscription, including its callbacks; repeated unsubscribe calls are harmless.

The low-level API makes connection initiation explicit:

```typescript
const rt = client.realtime();
rt.on('onError', (error) => console.error(error));
const subId = rt.subscribe(
  { query: { collection: 'users' } },
  { onEvent: (event) => console.log(event) },
);
await rt.connect();

rt.unsubscribe(subId);
rt.disconnect();
```

Concurrent `connect()` calls share one attempt. The promise resolves only after
`auth_ack`; subscriptions wait locally until authentication succeeds. WebSocket
authentication sends an `auth` message containing the token and database.

| API or event | Behavior |
|---|---|
| `rt.subscribe(options, callbacks?)` | Return a `subId` synchronously; register immediately if authenticated, otherwise retain locally |
| `rt.on(name, callback)` | Set one global observer for that event; subscription callbacks remain independent |
| Event or snapshot | Route by `subId` to its active subscription and the global observer; ignore messages for removed subscriptions |
| Registration error | Notify the matching subscription and global error observer; retain the subscription for later reconnect |
| Connection or authentication failure | Notify active subscriptions and the global error observer once for that failed attempt |
| Last unsubscribe | Leave the client-owned connection open |
| `disconnect()` | Stop socket and timers, reject a pending connection, retain subscriptions and callbacks for explicit reconnect |
| `dispose()` | Stop transport work and permanently clear subscriptions and observers; reuse is an error |
| `client.logout()` | Dispose its WebSocket client before running authentication logout |

Disconnect and disposal are repeatable. Late socket messages, authentication
results, and reconnect timers cannot revive a stopped connection. Synchronous
callback exceptions are reported separately from message parsing to the global
error observer (or logged if absent) and do not interrupt other eligible callbacks.
An exception in an error callback is logged. Callbacks returning promises must
handle their own asynchronous failures.

`RealtimeClientOptions.activityTimeoutMs` defaults to 90,000 ms. It bounds the
whole connection/authentication attempt even if heartbeats arrive, and detects
inactivity after authentication. Reconnect uses exponential backoff with jitter
(`reconnectDelayMs` default 1,000; `maxReconnectAttempts` default 5); authentication
success resets the attempt count. Only a structured `unauthorized` error matching
the current auth request triggers one token refresh. Invalid authentication,
missing tokens, refresh errors, and a repeated auth rejection fail the attempt.

WebSocket disposal does not cancel HTTP authentication requests or guarantee that
an in-flight refresh cannot restore credentials after logout. That shared-provider
limitation is tracked in the
[authentication session race proposal](../../.agents/notes/proposed/bug-fix/2026-09-10-sdk-authentication-session-race.md).

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

- SSE authentication is sent via Authorization header (sourced from the SDK token provider); query-string tokens are rejected.
