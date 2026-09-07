# Agent Note: Puller History Continuity and Recovery Boundaries

Status: implemented

## Problem

The former source recovery path deleted an invalid resume token and opened a
fresh watch. Replay did not verify a durable retention floor. A consumer could
therefore resume across unavailable history without learning that derived state
or external effects were incomplete. Idle-time gaps were also insufficient
evidence of loss because a quiet database produces the same timing pattern.

## Decision

Each durable log binds a stable source ID and collection scope to a persisted
generation. Committed and discarded sequence frontiers define its valid resume
range. Retention atomically deletes a contiguous prefix, associated identity
records, and advances the discarded frontier. Replay validates these facts using
the same safe snapshot as its records.

Authoritative MongoDB history-loss errors persist discontinuity and stop that
backend. Restart refuses a discontinuous log. Old generations, expired positions,
unknown sources, ahead positions, and unsupported formats return typed errors
locally and equivalent `syntrix.puller` ErrorInfo reasons remotely. An affected
aggregate subscription stops without advancing across the gap; unaffected logs
remain available for deliberate recovery.

First-run `from_now` durably records a session operation-time boundary before
watch readiness. `from_beginning` anchors the earliest retained oplog entry and
requires read permission for `local.oplog.rs`. These boundaries survive a restart
before the first event token is committed. Reconnect flushes admitted work before
choosing its durable source boundary.

Existing unversioned logs or changed source/scope bindings are rejected. No
runtime guess converts timestamp/hash keys into ingestion sequence. The
[operator procedure](../../../../docs/design/server/puller/01.architecture.md#shutdown-and-operator-controlled-reset)
requires stopping owners, preserving old state, intentionally initializing a new
generation, and rebuilding/reconciling affected consumers before accepting new
checkpoints. A missing checkpoint is not a substitute for successful recovery.

## Alternatives

**Increase retention.** This reduces expiry frequency but cannot cover every
outage, explicit eviction, incompatible format, or invalid resume token.

**Rebuild after every long event interval.** This mistakes inactivity for lost
history and still cannot identify shorter discontinuities.

**Reset and resume automatically.** This restores traffic but falsely treats a
new history as continuous and cannot reconstruct historical Webhook effects.

## Consequences

This delivery establishes detection, error propagation, and an operator-controlled
reset boundary. Automated full consumer recovery remains substantive work:
[Indexer rebuild](../../proposed/architecture/2026-09-07-indexer-recovery-lifecycle.md),
[Streamer durable progress](../../proposed/architecture/2026-09-07-streamer-durable-progress.md),
[client resynchronization](../../proposed/feature/2026-09-07-realtime-client-resume.md),
and [Trigger durable scheduling](../../proposed/architecture/2026-09-07-trigger-delivery-idempotency.md).
Until those workflows exist, a history failure stops service and requires an
operator decision; it does not claim an automatic rebuild or recovered side
effects. Preserving source/generation positions and explicit errors keeps those
workflows able to establish a trustworthy scan/replay boundary later.

Log format and consumer checkpoints require coordinated rollout. An incompatible
upgrade needs deliberate reset/rebuild or migration from authoritative ordered
history. A new generation invalidates old markers. See
[publication](2026-09-07-puller-persist-before-publish.md) for atomic event identity
and ordering, and [subscriptions](2026-09-07-local-puller-subscription-replay.md)
for delivery and terminal error ownership.
