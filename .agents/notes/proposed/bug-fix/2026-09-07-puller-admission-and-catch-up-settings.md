# Agent Note: Trigger Puller Catch-Up from Measured Lag

Status: proposed

## Problem

Active-subscription admission, unique registration identity, server/caller
coalescing permission, and replay budgets are now enforced by the
[shared subscription](../../../../internal/puller/core/subscriber.go).
`catch_up_threshold` selects coalescing during replay. A live subscriber still
switches to replay only after queue overflow or a detected sequence gap, so a
configured lag threshold does not proactively move a buffered live backlog into
catch-up. Operators cannot choose that earlier transition independently of queue
capacity.

## Proposal

Define how aggregate lag is measured across source-local committed positions and
apply `catch_up_threshold` to proactive live-to-replay transition. Bound sampling
frequency and cost; compute from sequence frontiers when possible without reading
payloads or repeatedly scanning the log. Preserve overflow as an immediate trigger
and preserve server permission plus request opt-in for coalescing.

Keep the existing registration cut and last-delivered checkpoint as the handoff
boundary. A threshold transition must not advance progress over queued but
undelivered records. Document whether the setting is source-local or aggregate,
its interaction with replay coalescing, and effective operator policy.

## Alternatives

**Keep overflow-only switching.** This avoids lag sampling and already bounds
memory. It remains appropriate if load measurements show no material benefit
from early replay; document the setting solely as a coalescing threshold in that
case.

**Coalesce every replay.** This reduces backlog traffic but changes event
semantics for consumers that require intermediate transitions. Existing dual
permission remains mandatory.

## Acceptance Criteria

- A controlled live backlog crosses the configured lag threshold and switches
  to replay without requiring queue overflow or skipping undelivered records.
- Sampling remains bounded with many subscribers and multiple source frontiers.
- Exact consumers retain every event; coalescing requires both permissions.
- Local and remote consumers have identical transitions and failure semantics.

## Risks

Sampling and extra replay can cost more than draining an existing memory queue.
Deferral preserves correctness and admission bounds, but leaves queue size as the
only operator control over early live backlog recovery. The shared coordinator,
source-local sequence positions, and fixed replay cuts keep a future transition
policy local to subscription selection rather than requiring a new delivery
protocol.

## Dependencies

[Shared subscriptions](../../implemented/architecture/2026-09-07-local-puller-subscription-replay.md)
own handoff and delivered admission policy;
[durable publication](../../implemented/architecture/2026-09-07-puller-persist-before-publish.md)
provides the committed frontier for lag measurement.
