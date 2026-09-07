# Agent Note: Puller History-Gap Recovery

Status: proposed

## Problem

Consumers cannot distinguish a complete resume from a stream whose history is
unavailable. [Recovery](../../../../internal/puller/recovery/recovery.go#L222)
calls `h.checkpoint.DeleteCheckpoint()` after resume-token errors; the backend
then opens a fresh watch. Meanwhile,
[Replay](../../../../internal/puller/core/puller.go#L443) scans after the supplied
key without checking a durable retention boundary, although the
[cleaner](../../../../internal/puller/buffer/cleaner.go) evicts history.

The call `backend.gapDetector.RecordEvent(evt)` in
[ingestion](../../../../internal/puller/core/puller.go#L373) ignores its boolean
result. That detector measures elapsed event time; quiet databases can produce
the same interval as lost history. Its warning alone cannot establish loss.
These findings come from static inspection.

## Proposal

Persist a continuity generation and replayable lower boundary per backend using
the monotonically increasing committed positions owned by
[persist before publish](2026-09-07-puller-persist-before-publish.md). Progress
markers carry the backend's generation and committed position; stable event
identity is preserved separately from ordering. Retention eviction advances the
position boundary atomically with deletion. Resume-token history loss marks that
backend discontinuous before fresh events can be published; positions from
different generations cannot be compared. Reject markers from an invalid
generation or an evicted interval with a structured
history-unavailable result identifying the affected backend and recovery reason.
Keep idle-time warnings diagnostic; confirm continuity failure through storage
history or an authoritative change-stream failure.

Expose the same result locally and over gRPC. Consumers stop advancing their
checkpoint and invoke their own recovery policy: Indexer rebuild, Streamer
subscription resynchronization, and an explicit Trigger lost-history incident.
A current snapshot cannot reconstruct historical webhook side effects. Migrate
existing timestamp/hash buffers and progress files through authoritative ordered
upstream history when available; sorting the old keys cannot recover ingestion
order. Otherwise invalidate the generation and require deliberate rebuild or
resynchronization rather than translate an unverifiable cursor. Invalidate old
markers explicitly through this migration, without runtime compatibility guesses.

## Alternatives

**Increase retention.** This reduces expiry frequency but cannot cover every
outage, explicit eviction, or invalid upstream resume token.

**Rebuild on every long event interval.** This requires little new metadata but
turns normal inactivity into expensive recovery and still misses shorter losses.

## Acceptance Criteria

- Retention eviction, upstream history loss, and restart each preserve a durable
  boundary; a stale marker never resumes as if continuity were intact.
- A quiet backend resumes normally when its history remains valid.
- Multi-backend markers identify the failed backend; unaffected database state
  is preserved while each consumer reports its required recovery action.
- Crash tests around eviction and generation changes expose either the prior
  valid state or the new discontinuity, never a falsely continuous stream.
- Equal-timestamp events with reversed event-ID/hash order remain ordered by
  committed position after restart. Retention boundaries use those positions,
  and a cursor from before migration is either verifiably migrated or rejected
  without skipping the later-ingested event.
- Local and remote callers receive the same failure meaning and cannot advance
  their processed checkpoint across the gap.

## Risks

Metadata and marker changes require coordinated consumer rollout. Rebuilds cost
storage reads; webhook history may be irrecoverable. Record backend, generation,
recovery reason, and affected consumer identity without raw tokens or payloads.

## Dependencies

[Local subscription replay](2026-09-07-local-puller-subscription-replay.md) carries
errors to local callers;
[persist before publish](2026-09-07-puller-persist-before-publish.md) owns durable
position assignment and stable upstream event identity;
[Indexer recovery](2026-09-07-indexer-recovery-lifecycle.md) and
[Streamer durable progress](2026-09-07-streamer-durable-progress.md) own their
recovery workflows.
