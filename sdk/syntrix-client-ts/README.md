# Syntrix TypeScript Client

## Installation

```bash
bun add @syntrix/client
```

## Usage

### SyntrixClient

```typescript
import { SyntrixClient } from '@syntrix/client';

const client = new SyntrixClient('http://localhost:8080', {
  token: 'my-token',
  refreshUrl: 'http://localhost:8080/auth/refresh',
  refreshToken: 'my-refresh-token'
});

const doc = await client.collection('users').doc('123').get();
```

### TriggerClient

```typescript
import { TriggerClient } from '@syntrix/client';

const client = new TriggerClient('http://localhost:8080', 'pre-issued-token');

await client.collection('users').doc('123').set({ name: 'Alice' });
```

## Realtime Subscriptions

```typescript
const client = new SyntrixClient('http://localhost:8080', {
  database: 'my-database',
  auth: { token: 'my-token' },
});

const subscription = client.subscribe('users', {
  onReady: () => schedulePull(),
  onEvent: (event) => console.log(event),
  onError: (error) => console.error(error),
});

subscription.unsubscribe();
client.realtime().dispose();
```

Convenience subscriptions share one automatically connected WebSocket and have
independent callbacks. `onReady` signals registration after authentication, both
initially and after reconnect; schedule reconciliation there when missed changes
must be fetched. Readiness does not mean historical data or a snapshot is complete.

The last unsubscribe leaves the connection open. Use `disconnect()` to stop it
while retaining subscriptions for explicit reconnect, or `dispose()` for permanent
cleanup. Logout disposes the client's WebSocket. Low-level `realtime().subscribe()`
requires an explicit `connect()`; its promise resolves after authentication.
See the [SDK reference](../../docs/reference/typescript_sdk.md#4-realtime-ws--sse)
for error routing, timeouts, and the separate shared authentication race limitation.

## Offline Replication (WIP)

Durable offline replication features are currently in development.
