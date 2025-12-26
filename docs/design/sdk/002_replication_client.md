# Replication Client Design (RxDB + Syntrix replication/realtime)

**Status:** Planned

## Context & Why
- We need offline-first replication for web clients using RxDB as local store.
- Server exposes HTTP replication (`/replication/v1/pull`, `/replication/v1/push`) and realtime change signal (`/realtime/ws`, `/realtime/sse`).
- Checkpoint is authoritative only in pull responses; realtime events are triggers, not state.

**Related:** Authentication flows and retry semantics are defined in [003_authentication.md](003_authentication.md); replication uses the same token/refresh handling and does not advance checkpoints on auth errors.

## Goals
- Reliable pull/push replication using RxDB, respecting server wire format (flattened docs, no storage internals).
- Realtime events only trigger pulls; checkpoint managed solely by pull responses.
- Conflict-safe push with server-returned conflicts written back or surfaced.
- Offline tolerance: queued pushes (outbox), resumable pulls with checkpoint persistence.

## Non-Goals
- New server endpoints or protocol changes.
- Rich conflict resolution UI/strategies (provide hooks only).
- Full-text/local secondary indexes beyond RxDB schema basics.

## Assumptions
- `/realtime/ws` (or `/realtime/sse`) can subscribe per collection and delivers at least `{ collection, id, action, updatedAt, deleted? }` plus some monotonic seq/lsn (used only for diagnostics; not trusted as checkpoint).
- HTTP replication per docs/reference/replication.md and design/003_01_replication.md.
- Token-based auth reusable for realtime channel; reconnect allowed.
- RxDB available (Dexie storage) in client environment.

## Data Model (flattened)
- Fields: `id`, `version?`, `updatedAt`, `createdAt`, `collection`, `deleted?`, plus user fields.
- RxDB primary key: `id`. Indexes: `updatedAt`, `collection`, optionally business fields.
- Tombstones: keep `deleted: true` docs with timestamps for cleanup/filters.

## Components
- **ReplicationCoordinator**: high-level orchestrator per collection; owns pull/push workers, realtime trigger wiring, state, callbacks.
- **PullWorker**: executes `/replication/v1/pull` with `{collection, checkpoint, limit}`; writes results into RxDB; updates CheckpointStore.
- **PushWorker**: drains Outbox to `/replication/v1/push`; handles conflicts by writing server docs or invoking conflict hook.
- **RealtimeTrigger**: listens `/realtime/ws` (or `/realtime/sse`); enqueues pull requests (no checkpoint from event).
- **CheckpointStore**: persists per-collection checkpoint (stringified int64) locally (RxDB key-value or storage adapter).
- **Outbox**: local queue of pending writes (create/update/replace/delete), durable across reloads.

## SDK Architecture (public surface)
- Package entry: `pkg/syntrix-client-ts/src/index.ts` re-exports clients and will export replication orchestrator types once implemented.
- Public clients remain:
	- `SyntrixClient` for CRUD/query over HTTP.
	- `TriggerClient` for trigger writes.
	- `TriggerHandler` wrapper for trigger payload execution.
- New replication surface (planned):
	- `createReplicationCoordinator(options): ReplicationCoordinator` factory.
	- Interfaces: `ReplicationOptions`, `PullOptions`, `PushOptions`, `RealtimeOptions`, `CheckpointStore`, `OutboxAdapter`, hooks types.
- Suggested layout under `src/replication/`:
	- `coordinator.ts` (orchestrator, public entry)
	- `pull.ts` (PullWorker)
	- `push.ts` (PushWorker)
	- `realtime.ts` (trigger wiring abstraction)
	- `checkpoint.ts`, `outbox.ts` (pluggable adapters)
	- `types.ts` (options, hooks, DTOs)

### High-level call graph (SDK)
```
App
 |- SyntrixClient (HTTP CRUD/query)
 |- createReplicationCoordinator({...})
			|- PullWorker -> /replication/v1/pull -> RxDB collections
			|- PushWorker -> /replication/v1/push -> Outbox mgmt
			|- RealtimeTrigger -> schedules PullWorker
			|- CheckpointStore / OutboxAdapter -> persistence
```

### Initialization flow
1) App constructs `SyntrixClient` (baseURL, token) and RxDB database with collections.
2) App calls `createReplicationCoordinator({ collection, client, rxdbCollection, checkpointStore, outboxAdapter, realtime, hooks, backoff, limits })`.
3) Coordinator wires realtime subscription (if enabled), starts a safety pull timer, and optionally runs an initial pull.
4) App writes go through RxDB and enqueue to Outbox (via helper we provide or explicit call). PushWorker drains automatically.

### Public usage patterns
- **Online-first read**: keep using `SyntrixClient.query` for server truth.
- **Offline-first read**: read from RxDB directly; coordinator keeps it synced.
- **Write**: write to RxDB + Outbox helper; PushWorker syncs; conflicts surfaced via hook.
- **Control**: expose `start()`, `pause()`, `resume()`, `shutdown()` on coordinator for lifecycle (e.g., tab visibility, logout).
- **Metrics/diagnostics**: hooks emit pull/push timings, counts, last checkpoint for observability.

## Control Flow
```
[realtime event] -> [trigger queue] --(throttle 200-500ms)--> [PullWorker]
[interval timer]  ------------------------------------------^
[app writes] -> [Outbox] -> [PushWorker]
```

### Pull sequence (happy path)
1) Determine `checkpoint` from CheckpointStore (default "0").
2) Call `/replication/v1/pull?collection=...&checkpoint=...&limit=...`.
3) Upsert returned documents into RxDB; preserve `deleted` tombstones.
4) Persist returned `checkpoint` for next cycle.
5) Emit callbacks `onPullSuccess` with counts/timing.

### Push sequence
1) Read batch from Outbox (bounded size).
2) Send `/replication/v1/push` with `{collection, changes}`.
3) On success, remove sent entries from Outbox.
4) If `conflicts` returned, upsert them to RxDB and emit `onConflict(conflicts, locals?)`.
5) Errors: retry with backoff, keep Outbox intact.

### Realtime trigger policy
- Event arrival only schedules a pull; event seq/lsn is not persisted as checkpoint.
- Multiple events coalesced via throttle/debounce to a single pull.
- On reconnect: immediately run a pull with last checkpoint, then resume listening.
- Periodic safety pull (e.g., every N minutes) to cover missed events.

## Error Handling & Resilience
- Pull errors: exponential backoff with jitter; checkpoint unchanged until success.
- Push errors: retain Outbox, retry with backoff; optionally surface fatal 4xx to app.
- Network loss: pause realtime, keep Outbox; on regain, run pull then resume push.
- Idempotency: RxDB upserts keyed by `id`; `updatedAt/version` prevent regression (drop stale writes if local version < incoming version when applicable).

## Observability Hooks
- `onPullScheduled(reason)`, `onPullSuccess(stats)`, `onPullError(err)`
- `onPushSuccess(batchInfo)`, `onPushError(err)`
- `onConflict(conflicts, locals?)`

## Configuration Surface (draft)
- `collection`: string (required)
- `pullLimit`: number (default 200–500)
- `pullThrottleMs`: number (e.g., 200–500)
- `safetyPullIntervalMs`: optional periodic pull
- `backoff`: { baseMs, maxMs, factor, jitter }
- `checkpointStore`: pluggable (default RxDB key-value)
- `outboxAdapter`: pluggable (default RxDB collection)
- `realtime`: { enable: boolean, subscribe: fn, unsubscribe: fn }
- `hooks`: callbacks listed above

## Conflict Handling Options
- Default: server-wins (upsert conflicts, clear outbox entries for those ids).
- Custom: app-provided merge in `onConflict`, then enqueue merged doc back to Outbox for retry.

## Cleanup
- Tombstone GC (optional): app can provide policy (e.g., delete tombstones older than N days after last checkpoint synced) to keep local store small.

## Connection Health & Keepalive

### Server-side (WebSocket)
- **Ping interval:** 54 seconds (`pongWait * 9/10`)
- **Pong timeout:** 60 seconds - connection closed if no pong received
- **Write timeout:** 10 seconds per message

The server sends WebSocket Ping frames; browsers automatically respond with Pong. If the server doesn't receive a Pong within 60 seconds, it closes the connection.

### Server-side (SSE)
- **Heartbeat interval:** 15 seconds - server sends `: heartbeat\n\n` comments
- Client should monitor incoming data; if no data (including heartbeats) arrives for an extended period, consider reconnecting.

### Client-side Keepalive (SDK)

- Browser WebSocket API automatically responds to server Ping frames
- Client tracks `lastMessageTime` on every incoming message (including server heartbeats)
- **Activity timeout:** If no messages received within `activityTimeoutMs` (default: 90s), proactively close and trigger reconnect
- Reconnect uses exponential backoff: base delay × 2^(attempt-1), capped at max attempts

### Reconnect Flow
1. On disconnect detected (via `onclose` or activity timeout), set state to `disconnected`.
2. Attempt reconnect with exponential backoff.
3. On successful reconnect:
   - Re-authenticate (send auth message with fresh token)
   - Re-subscribe to all active subscriptions
   - Trigger immediate pull to catch up on missed changes
4. If max retries exceeded, stop and invoke `onError` hook.

### Configuration (planned)
```typescript
interface RealtimeOptions {
  maxReconnectAttempts?: number;  // default: 5
  reconnectDelayMs?: number;      // base delay, default: 1000
  activityTimeoutMs?: number;     // client-side health check, default: 90000
}
```

## Security
- Reuse bearer token for HTTP and realtime; refresh hooks must be supported before retry.
- Validate collection names client-side before requests (defensive against misuse).

## Open Questions / Decisions
- Exact realtime payload shape and subscribe API (event source vs websocket vs SSE). We only require it can signal per-collection changes.
- Should push include client `updatedAt/version` always, or only when available? (current protocol allows optional version).
- Outbox persistence format: per-collection vs global queue; proposed per-collection for simpler retries.

## Testing Plan (to implement with the code)
- Pull: checkpoint advance, tombstone handling, throttle coalescing, backoff on failures.
- Push: outbox drain, retry/backoff, conflict upsert, idempotent duplicate suppression.
- Realtime trigger: event-driven pull scheduling, debounce, reconnect flow (pull-then-subscribe), safety interval coverage.
- Concurrency: simultaneous pull/push without corrupting checkpoint or outbox.
- Persistence: checkpoint/outbox survive reload, resume correctly.
- Error paths: auth failure, 4xx on push, transient network failures on pull.

## Next Steps
- Define TypeScript interfaces for the components above.
- Add ASCII sequence diagrams to code comments when implementing orchestrator.
- Implement and unit-test orchestrator, pull/push handlers, outbox/checkpoint adapters.
