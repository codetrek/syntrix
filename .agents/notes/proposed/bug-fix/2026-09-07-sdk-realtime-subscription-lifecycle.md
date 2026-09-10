# Agent Note: SDK Realtime Subscription Lifecycle

Status: proposed

## Problem

The [SDK convenience method](../../../../sdk/syntrix-client-ts/src/clients/syntrix-client.ts) creates subscriptions but never connects the transport. A caller using only `client.subscribe(...)` therefore receives no realtime notifications.

Each convenience subscription also replaces the shared transport callback: [RealtimeClient.on](../../../../sdk/syntrix-client-ts/src/replication/realtime.ts) assigns `this.callbacks[event] = callback`. Multiple subscriptions therefore compete for one event callback. Returned unsubscribe handles remove remote registrations without unregistering these callbacks.

These SDK defects remain after [retiring the Chat example](../../implemented/simplification/2026-09-10-remove-chat-example.md). The removed application's RxDB and session lifecycle are no longer part of this proposal.

## Proposal

Give the convenience subscription API an explicit connection lifecycle and subscription-scoped callback ownership. The first active subscription should start one authenticated connection, pending subscriptions should be sent after authentication, and returned handles must unregister their callbacks as well as their remote subscriptions. Route events and snapshots by subscription identifier; route connection failures through an explicit shared lifecycle channel or consistently notify active subscribers.

Expose subscription readiness after initial registration and re-registration so each active subscriber can schedule its own reconciliation. Connection recovery must not imply that missed events were delivered. Subscription teardown and client disposal must release their callbacks, registrations, sockets, and timers according to their ownership.

This proposal owns convenience API connection and callback lifecycle. Durable outbox, checkpoint persistence, and bounded synchronization workers belong to SDK offline replication; replay continuity belongs to realtime resume.

## Alternatives

**One socket per subscription:** naturally separates callbacks but multiplies authentication, heartbeats, and reconnection load as applications add subscriptions.

**One application callback that resynchronizes everything:** can restore updates but turns every event into work for unrelated collections. Subscription-scoped dispatch preserves targeted synchronization and clear cleanup.

## Acceptance Criteria

- Convenience subscriptions establish one owned connection and register after authentication.
- Events and snapshots for two subscriptions invoke their own callbacks; unsubscribing one leaves the other functioning.
- Initial registration and re-registration expose readiness for every active subscription without claiming delivery continuity.
- Unsubscribe stops that subscription's callbacks; client disposal stops all owned callbacks and transport work. Repeated start/stop cycles leave no sockets or timers.

## Dependencies

[Realtime resume](../feature/2026-09-07-realtime-client-resume.md) owns transport recovery guarantees. [SDK offline replication](../feature/2026-09-07-sdk-offline-replication.md) owns durable synchronization and conflict handling.

## Risks

Automatic connection changes the convenience API's observable lifecycle. Authentication failures and callback exceptions need explicit propagation, and subscription teardown must not disconnect other active consumers.
