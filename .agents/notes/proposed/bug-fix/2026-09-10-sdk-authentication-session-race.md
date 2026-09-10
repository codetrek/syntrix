# Agent Note: SDK Authentication Session Race

Status: proposed

## Problem

An in-flight token refresh can complete after logout clears credentials and
restore the old session. The
[default token provider](../../../../sdk/syntrix-client-ts/src/internal/auth/provider.ts)
stores refreshed access and refresh tokens and invokes `onTokenRefresh` before
its refresh promise settles. A transport that ignores the late promise result
cannot prevent those mutations or callbacks.

## Proposal

Define session ownership for credential-changing operations in the shared token
provider. A refresh belonging to a logged-out or replaced session must not mutate
current credentials or emit a successful token-refresh callback. Determine how
waiting consumers observe an invalidated operation, and apply that rule to HTTP
interceptors, WebSocket authentication, SSE, and login/session replacement.

The [WebSocket lifecycle decision](../../implemented/bug-fix/2026-09-07-sdk-realtime-subscription-lifecycle.md)
stops disposed transports from restarting. Preserve that guard; it does not provide
the shared credential guarantee. The authentication provider and its consumers
need separate tests and coordination, without coupling credential ownership to
any one transport's lifetime.

## Alternatives

**Ignore late results only in WebSocket callbacks.** This prevents transport
revival, but credential mutation and refresh callbacks have already happened
inside the provider. It cannot enforce logout across other authentication users.

## Acceptance Criteria

- A refresh started before logout cannot restore credentials after logout or
  emit a successful token-refresh callback for that session.
- Replacing a session prevents an older refresh from overwriting its credentials.
- Concurrent refresh consumers receive a consistent, documented outcome when
  their session is invalidated; errors are not converted to successful tokens.
- Tests control request completion ordering and cover shared HTTP, WebSocket,
  and SSE authentication consumers.

## Risks

Until this guarantee is implemented, late refresh responses can restore
credentials after logout. Fixing it requires auditing shared provider mutations,
callbacks, and retry consumers; a socket-only fix cannot remove that work. Request
cancellation alone may race with a completed response, so credential ownership
must be checked at the mutation point. The exact invalidation mechanism remains
undecided.
