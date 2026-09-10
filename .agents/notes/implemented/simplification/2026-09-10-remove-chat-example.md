# Agent Note: Remove the Chat Example

Status: implemented

## Problem

The Chat example combined application-owned RxDB replication and session cleanup
with SDK realtime subscriptions whose connection and callback lifecycle was
incomplete. Keeping the example required maintaining both the application and
the shared SDK defects it exposed.

## Decision

Retire `example/chat-app/`, including its source, assets, package manifests,
lockfile, and application documentation. Remove active instructions that present
it as an available example. No replacement application is introduced.

The [SDK realtime subscription lifecycle proposal](../../proposed/bug-fix/2026-09-07-sdk-realtime-subscription-lifecycle.md)
retains the shared connection, callback, teardown, and reconnect-readiness work.
Application-specific RxDB and session cleanup obligations end with removal.

## Alternatives

**Repair the example's synchronization lifecycle.** This would retain a runnable
chat demonstration but require coordinated application and SDK maintenance.
The chosen scope retires the application; the reusable SDK defects remain
separately tracked.

## Consequences

- The repository no longer provides this Chat application or its setup workflow.
- Shared SDK behavior is unchanged. Removing a consumer does not fix its
  subscription defects or establish replay guarantees.
- Completing the SDK lifecycle still requires changes and tests in the SDK;
  a future application must own its local persistence and session lifecycle.
