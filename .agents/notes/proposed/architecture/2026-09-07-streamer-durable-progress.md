# Agent Note: Durable Streamer Consumption Progress

Status: proposed

## Problem

The [Streamer requirements](../../../../docs/design/server/streamer/00.requirements.md)
require replay after restart. The [service](../../../../internal/streamer/service.go)
now advances in-memory progress only after successful processing and closes its
owned gateway streams on terminal upstream failure. It still does not load or
persist that marker, so a new process subscribes with an empty start marker and
cannot replay the work missed across restart.

Subscriptions remain soft state. Gateway re-registration is implemented in
[the remote stream](../../../../internal/streamer/remote_stream.go), but it does
not establish event continuity for disconnected clients. Durable ingestion
progress and end-client recovery have separate responsibilities.

## Proposal

Persist the opaque Puller progress marker under a stable Streamer consumer
identity and load it before subscribing. Keep the aggregate marker intact;
logical databases must not overwrite separate portions independently. Establish
single-writer ownership of each checkpoint, with an explicit bootstrap policy
for a missing record and a surfaced error for unreadable or invalid state.

Advance durable progress only after an event reaches a defined processing
outcome. Transformation failures and interrupted processing retain the earlier
marker; intentionally ignored event categories must be distinguished from
failures. A gateway delivery timeout must explicitly invalidate that gateway's
continuity before ingestion can advance. Saving progress acknowledges Streamer
processing, not client receipt. Recovery may repeat events.

Serialize processing and checkpoint writes, stop consumption on unresolved
processing or persistence failure, and join the consumer during shutdown under
a bounded deadline. Record consumer
identity, checkpoint generation, event correlation, and failure category without
document bodies. Document the new checkpoint schema and bootstrap procedure.

## Alternatives

**Periodic checkpoints.** Batching reduces writes but increases replay after a
crash. It is worth reconsidering after measuring persistence cost, provided the
replay interval is bounded and duplicates remain safe.

**Per-client durable delivery queues.** These can retain deliveries after
disconnection, but introduce subscription storage and retention ownership beyond
the upstream consumption contract. Client continuity belongs to the linked
resume proposal.

## Acceptance Criteria

- A restart passes the last committed aggregate marker to local and remote
  Puller paths; alternating database events do not overwrite each other's
  progress.
- Injected transformation, checkpoint-write, and cancellation failures cannot
  commit progress past unresolved work; restart can replay it.
- Concurrent ownership is rejected, missing-state bootstrap is explicit, and
  expired history produces an actionable recovery state.
- Gateway timeout and Streamer restart cannot be reported as successful
  end-client continuity solely because a checkpoint exists.

## Risks

Persistence adds ingestion latency and operational state. Deferral leaves
restarts dependent on an empty marker; adding storage without defining the
acknowledgment boundary would preserve silent loss. Stable consumer identity
must survive deployment changes without allowing concurrent writers.

## Dependencies

[Local replay](../../implemented/architecture/2026-09-07-local-puller-subscription-replay.md) and
[history-gap recovery](../../implemented/architecture/2026-09-07-puller-history-gap-recovery.md) provide upstream
recovery. [Client resume](../feature/2026-09-07-realtime-client-resume.md) owns
downstream continuity; [consumer scaling](2026-09-07-consumer-shard-scaling.md)
owns future ownership transfer.
