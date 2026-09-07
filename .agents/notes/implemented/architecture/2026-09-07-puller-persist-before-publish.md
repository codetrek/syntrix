# Agent Note: Persist Puller Events Before Publication

Status: implemented

## Problem

Consumers could receive an event and save its progress while its Pebble write
was still pending. Replay also included pending memory records. A crash or disk
failure could therefore leave consumers ahead of the local durable history.
Timestamp/hash keys could place a later-ingested event behind a saved cursor,
so persistence alone could not establish a safe recovery sequence.

## Decision

The [buffer](../../../../internal/puller/buffer/buffer.go) has one mutation
owner per backend. It assigns monotonically increasing sequence positions within
a persisted source generation and atomically commits events, retained identity
mappings, the frontier, and MongoDB resume token with Pebble Sync. Event identity
uses the complete BSON source token and configured stable source ID, independent
of sequence. Retained duplicates reuse their original record and position.

A successful commit publishes a safe snapshot and then invokes the ordered
coordinator. Real-time consumers receive the immutable batch from memory;
catch-up reads bounded pages from the safe snapshot. Raw Pebble visibility and
pending writes are not replay authorities. The publication callback finishes
before admission credits are released or the next mutation starts.

The initial MongoDB operation-time boundary is persisted before watch readiness.
An admission fence flushes existing work before reconnecting. Decode,
normalization, storage, and continuity failures stop progress and retain their
error chain. Shutdown drains admitted work; a deadline can return while the
physical sync retains ownership of its resources.

The [architecture](../../../../docs/design/server/puller/01.architecture.md)
owns the full protocol, configuration, and reset procedure.

## Alternatives

**Sync every event immediately.** This offered a direct durability boundary but
paid a synchronization cost per event. Configurable batching preserves throughput
while making the durable completion explicit.

**Publish pending writes and rely on MongoDB replay.** This reduced latency but
let consumer checkpoints outrun the local durable log.

**Use timestamp and event-ID hash order.** Deterministic key sorting did not
represent ingestion order for equal timestamps and could exclude later events.

**Read Pebble for every delivery.** This would reduce handoff paths but add reads
and decoding to real-time delivery. The shared subscription coordinator gives
memory live delivery and Pebble replay the same committed sequence.

## Consequences

Batch waiting and synchronous storage latency are part of change-delivery
latency. Defaults remain configurable at 100 events and 100ms. Additional
identity and position metadata increases storage work. Delivery remains
at-least-once; a consumer must save progress after its own successful processing.

The stored format and opaque cursor semantics change together. Legacy nonempty
logs and old cursors are refused explicitly; recovery requires a deliberate
migration or a new generation with derived-state reconciliation. The
[continuity decision](2026-09-07-puller-history-gap-recovery.md) owns that boundary.
[Write budgets](../bug-fix/2026-09-07-puller-pending-write-bound.md) and
[shared subscriptions](2026-09-07-local-puller-subscription-replay.md) keep the
pending and live paths bounded.
