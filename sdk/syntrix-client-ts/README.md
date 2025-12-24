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

## Replication (WIP)

Replication features are currently in development.
