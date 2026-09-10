# Agent Note: Integrate Remaining Puller Consumer Settings

Status: proposed

## Problem

Operators can configure catch-up policies that do not govern subscriptions.
[Configuration](../../../../internal/puller/config/puller.go) defines
`CatchUpThreshold` and `Consumer.CoalesceOnCatchUp`, but production usage stops
at configuration initialization and validation. [gRPC Subscribe](../../../../internal/puller/grpc/server.go)
passes `req.GetCoalesceOnCatchUp()` directly into the subscriber. Its existing
overflow-driven replay works; the missing integration concerns configured lag
measurement and coalescing policy.

[gRPC admission](../../implemented/bug-fix/2026-09-07-puller-grpc-admission.md)
enforces `MaxConnections` as active `Subscribe` RPCs per Server. Local
subscriptions remain outside that quota and have a separate delivery loop.

## Proposal

Apply `catch_up_threshold` to the subscription's per-backend backlog. Its
measurement and reference position remain draft after rejection of the
[publication proposal](../../rejected/architecture/2026-09-07-puller-persist-before-publish.md).
Specify a bounded measurement frequency and retain
overflow as an immediate trigger. Server `coalesce_on_catch_up` permits
coalescing; the individual subscription must also opt in. This avoids silently
merging events for Trigger or other consumers needing each transition.

Carry these policies into the proposed shared local/remote subscription
mechanism and document effective values and precedence. Extending admission to
local subscriptions requires an explicit quota owner, counting unit, and local
rejection contract. Preserve the delivered gRPC quota while those decisions
remain open; any shared quota must count registrations independently of
diagnostic consumer labels, as required by
[subscription identity isolation](../../implemented/bug-fix/2026-09-09-puller-subscription-identity.md).

## Alternatives

**Remove the unused settings.** This makes configuration honest but removes
operator control over catch-up cost. Reconsider if load
tests show threshold measurement is more expensive than overflow-only replay.

**Coalesce every catch-up stream.** This reduces traffic but changes event
semantics for consumers that require intermediate transitions.

## Acceptance Criteria

- A controlled backlog crosses the configured threshold and enters catch-up
  without requiring channel overflow; backend progress remains independent.
- Coalescing occurs only when both server policy and the caller permit it;
  non-coalescing consumers retain every event in local and remote operation.
- Invalid settings fail validation, and documented settings survive configuration
  load through production service assembly to observable runtime behavior.
- Local admission exposes rejection to its caller under an explicitly defined
  quota, without weakening the existing per-Server gRPC limit or label isolation.

## Risks

Counting backlog can amplify disk reads across many consumers. Coalescing alters
the number of delivered events and needs explicit operational diagnostics.
Report effective policy, backlog, and transitions without raw cursor values or
payloads. Deferring local admission leaves in-process subscriptions unbounded by
the gRPC setting; adding it requires a local error contract and quota ownership
decision. Deferring lag policy leaves overflow as the runtime catch-up trigger;
its later implementation requires backlog measurement and policy integration.

## Dependencies

[Local subscription replay](../architecture/2026-09-07-local-puller-subscription-replay.md)
owns the proposed local/remote delivery integration. Lag measurement requires
its own confirmed definition; the rejected publication scheme supplies none.
