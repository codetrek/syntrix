# Agent Note: Enforce Puller Admission and Catch-Up Settings

Status: proposed

## Problem

Operators can configure policies that do not govern subscriptions.
[Configuration](../../../../internal/puller/config/puller.go) defines
`MaxConnections`, `CatchUpThreshold`, and `Consumer.CoalesceOnCatchUp`, but
production usage stops at configuration initialization and validation.
[gRPC Subscribe](../../../../internal/puller/grpc/server.go#L158) passes
`req.GetCoalesceOnCatchUp()` directly into the subscriber and admits it without
consulting MaxConnections. Its existing overflow-driven replay works; the
missing integration concerns the configured admission and lag policies.

## Proposal

Make the server limit active Puller subscriptions atomically, with capacity
released on every termination path. Define explicitly whether `max_connections`
retains its name as a subscription limit or is renamed with an operator config
migration; transport connection count must not stand in for streaming RPC count.
Build on the implemented [subscription identity isolation](../../implemented/bug-fix/2026-09-09-puller-subscription-identity.md):
capacity must count registrations independently of diagnostic consumer labels.

Apply `catch_up_threshold` to the subscription's per-backend backlog. Its
measurement and reference position remain draft after rejection of the
[publication proposal](../../rejected/architecture/2026-09-07-puller-persist-before-publish.md).
Specify a bounded measurement frequency and retain
overflow as an immediate trigger. Server `coalesce_on_catch_up` permits
coalescing; the individual subscription must also opt in. This avoids silently
merging events for Trigger or other consumers needing each transition. Carry
these policies into the shared local/remote subscription mechanism and document
effective values and precedence.

## Alternatives

**Remove the unused settings.** This makes configuration honest but removes
operator control over an existing memory and catch-up cost. Reconsider if load
tests show threshold measurement is more expensive than overflow-only replay.

**Coalesce every catch-up stream.** This reduces traffic but changes event
semantics for consumers that require intermediate transitions.

## Acceptance Criteria

- Concurrent admission accepts at most the configured number of subscriptions;
  overflow returns an explicit resource-exhausted error and cancellation frees
  capacity, including duplicate diagnostic consumer IDs.
- A controlled backlog crosses the configured threshold and enters catch-up
  without requiring channel overflow; backend progress remains independent.
- Coalescing occurs only when both server policy and the caller permit it;
  non-coalescing consumers retain every event in local and remote operation.
- Invalid settings fail validation, and documented settings survive configuration
  load through production service assembly to observable runtime behavior.

## Risks

Counting backlog can amplify disk reads across many consumers. Coalescing alters
the number of delivered events and needs explicit operational diagnostics.
Report admission rejection, effective policy, backlog, and transitions without
raw cursor values or payloads.

## Dependencies

[Local subscription replay](../architecture/2026-09-07-local-puller-subscription-replay.md)
owns the proposed local/remote delivery integration. Lag measurement requires
its own confirmed definition; the rejected publication scheme supplies none.
