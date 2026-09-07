# Agent Note: Chat Realtime Synchronization

Status: proposed

## Problem

The [chat replication setup](../../../../example/chat-app/src/db.ts) calls `client.subscribe(...)` to schedule `replicationState.reSync()`. The [SDK convenience method](../../../../sdk/syntrix-client-ts/src/clients/syntrix-client.ts) creates subscriptions but never connects the transport. The chat client does not establish that connection elsewhere.

Each convenience subscription also replaces the shared transport callback: [RealtimeClient.on](../../../../sdk/syntrix-client-ts/src/replication/realtime.ts) assigns `this.callbacks[event] = callback`. Multiple chat collections therefore compete for one event callback. Static inspection confirms the connection and callback ownership gaps; the application has not been exercised to reproduce them here.

## Proposal

Give the convenience subscription API an explicit connection lifecycle and subscription-scoped callback ownership. The first active subscription should start one authenticated connection, pending subscriptions should be sent after authentication, and returned handles must unregister their callbacks as well as their remote subscriptions. Route events and snapshots by subscription identifier; route connection failures through an explicit shared lifecycle channel or consistently notify active subscribers.

Have the chat application own and dispose its replication handles together with the authenticated client. Reconnect must trigger catch-up for every active collection, and logout or client reset must cancel replication, release subscriptions, and disconnect the old client before another account begins work. Coalesce repeated event signals to avoid concurrent unbounded resynchronization.

This change repairs event-driven synchronization ownership. The reusable durable outbox and checkpoint implementation belongs to the SDK offline proposal; the chat example should adopt that contract when available rather than become a separate owner of replication semantics.

## Alternatives

**One socket per collection:** naturally separates callbacks but multiplies authentication, heartbeats, and reconnection load as chats create dynamic collections.

**One application callback that resynchronizes everything:** can restore updates but turns every event into work for unrelated collections. Subscription-scoped dispatch preserves targeted synchronization and clear cleanup.

## Acceptance Criteria

- Opening the chat with a valid session establishes exactly one owned connection and registers all active collections after authentication.
- Events for two collections invoke their own callbacks; unsubscribing one leaves the other functioning.
- A connection interruption triggers catch-up for every remaining collection after reconnect.
- Logout, account replacement, and cancellation stop previous callbacks and requests; repeated start/stop cycles leave no sockets or timers.

## Dependencies

[Realtime resume](../feature/2026-09-07-realtime-client-resume.md) owns transport recovery guarantees. [SDK offline replication](../feature/2026-09-07-sdk-offline-replication.md) owns durable synchronization and conflict handling.

## Risks

Automatic connection changes the convenience API's observable lifecycle. Authentication failures and callback exceptions need explicit propagation, and subscription teardown must not disconnect other active consumers.
