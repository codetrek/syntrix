# SDK Authentication Design

**Date:** December 22, 2025
**Status:** Planned

## Context & Why
- The SDK needs a consistent auth story across HTTP CRUD/query, replication (pull/push), and realtime channels.
- We must support short-lived bearer tokens with refresh, while keeping secrets (refresh tokens/API keys) out of the SDK internals.
- Replication and realtime must not diverge in auth handling; retries and refresh should be predictable and bounded.

## Goals
- Single, pluggable auth abstraction reused by `SyntrixClient`, replication workers, and realtime subscription.
- Safe refresh handling: serialize refresh, retry once on 401/403, avoid infinite loops.
- Token injection without leaking or persisting sensitive credentials in the SDK.
- Clear hooks so host apps control storage of refresh tokens and decide logout vs. retry.

## Non-Goals
- Defining server-side auth; this is client-only wiring.
- Managing user sessions or UI flows (login/consent) inside the SDK.
- Persisting refresh tokens/API keys in the SDK; caller owns secret storage.

## Auth Surface (proposed)
- `TokenProvider: () => Promise<string>`: fetches the latest access token (may trigger refresh upstream).
- `AuthHooks`: `{ onAuthError?, onTokenRefreshed?, onAuthRetry?, onRealtimeAuthError? }`.
- `setToken(token: string)`: mutable setter for simple API-key or pre-fetched token flows.
- `AuthConfig`: `{ tokenProvider?, staticToken?, refresh?: () => Promise<string>, maxAuthRetries?: 1 }` (refresh optional; if omitted, fail fast on 401/403).

## Request Injection
- Axios interceptor fetches token via `tokenProvider` (preferred) or `staticToken`.
- Attach `Authorization: Bearer <token>` to every request; avoid caching tokens longer than necessary.
- Never log tokens; only emit sanitized diagnostics via hooks.

## Refresh & Retry Policy
- On 401/403: if `refresh` provided, serialize refresh (single in-flight), update token via `setToken`, then retry the failed request once.
- If refresh fails or no refresh provided: surface error via `onAuthError` and propagate to caller.
- Network errors follow existing backoff; auth errors do not exponential-backoff (they need user/token action).

## Realtime Channel (/realtime/ws, /realtime/sse)
- Shares the same `tokenProvider`/`refresh` strategy.
- On auth failure: close the channel, invoke `onRealtimeAuthError`, let caller decide when to resume (after refreshing token). Avoid infinite reconnects without new token.

## Replication (pull/push)
- Pull/Push use the same auth layer; retries on 401/403 follow the refresh-once rule.
- Do not advance checkpoints on auth failures; resume after refresh with the last persisted checkpoint.

## Trigger Handler
- `TriggerHandler` continues to require `preIssuedToken` from the payload; no auto-refresh. Fail fast if missing/invalid.

## Multi-tenant / Audience
- Prefer token-scoped tenancy. If a tenant header is ever needed, expose an explicit option (not implicit) to avoid drift between token and header.

## Concurrency & Safety
- Refresh is serialized; queued requests wait for the refreshed token and reuse it.
- Cap auth retries per request to 1 to avoid loops.

## Observability
- Hooks fire with sanitized metadata (no tokens):
  - `onAuthError({ endpoint, status })`
  - `onTokenRefreshed()`
  - `onAuthRetry({ endpoint })`
  - `onRealtimeAuthError({ reason })`

## Testing Plan
- 401 on CRUD: trigger refresh -> retry succeeds.
- 401 on CRUD without refresh: propagate error, no retry.
- Refresh failure: single retry attempt, then error, hook fired.
- Concurrency: multiple parallel requests hit 401 -> only one refresh occurs; all retry once with new token.
- Realtime: auth failure closes channel, fires hook; after `setToken` and `resume()`, subscription recovers.
- Replication pull/push: 401 triggers refresh-once, checkpoint unchanged on failure, resumes on success.

## Integration Points
- `001_sdk_architecture.md`: Auth abstraction shared by `SyntrixClient` and `TriggerClient` (where applicable), with clear separation of trigger pre-issued tokens.
- `002_replication_client.md`: Replication workers use the same auth layer; realtime trigger also relies on it; no checkpoint advance on auth errors.
