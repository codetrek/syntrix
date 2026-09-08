# Agent Note: Bound Puller Pending Writes

Status: proposed

## Problem

The configured write queue does not bound admitted work.
[Buffer construction](../../../../internal/puller/buffer/buffer.go) stores
`queueSize: queueSize`, but the value has no subsequent production read.
[Write](../../../../internal/puller/buffer/writer.go#L51) always performs
`b.pending = append(b.pending, req)` while the background batcher may be blocked
on disk. Under sustained ingestion, pending events can therefore grow beyond
`Buffer.QueueSize`. This is a source-level finding; no load measurement is
claimed.

## Proposal

Define `queue_size` as a per-backend limit on admitted events awaiting durable
completion, counting both pending and currently flushing events. Acquire
capacity before retaining the encoded payload, block ingestion when full, and
release capacity only after successful completion or a terminal failure.
Waiting admission must accept cancellation and return the original backend
failure when the writer fails. Closing the buffer wakes waiters and rejects new
admission.

Bound individual batch construction by `batch_size`, even after a burst. Preserve
event order and rely on the upstream durable resume token when ingestion pauses.
Document that an event-count limit is not a byte limit: aggregate memory also
depends on event size, backend count, and subscriber buffers. Evaluate a byte
budget only if measured event-size variance makes the existing count-based
contract insufficient.

## Alternatives

**Return overload immediately.** This bounds memory but terminates capture under
the implemented [admission-failure handling](../../implemented/bug-fix/2026-09-08-puller-capture-failures.md).
Blocking admission keeps temporary capacity exhaustion within normal backpressure.

**Allocate a buffered channel of queue_size.** A channel simplifies waiting, but
its capacity alone excludes the in-flight batch and does not bound admission
before payload allocation. It is suitable only with matching capacity accounting.

## Acceptance Criteria

- With a stalled commit and concurrent writers, pending plus flushing events
  never exceed queue_size; batches never exceed batch_size.
- Canceling blocked admission returns promptly without consuming capacity or
  admitting its event.
- Commit failure and shutdown release all blocked writers with the appropriate
  error and leave no background goroutines running after the test timeout.
- Independent backends enforce separate limits, retain backend event order, and
  resume from their durable checkpoint after a stall and restart.

## Risks

Backpressure can let MongoDB history expire during prolonged disk failure; the
operator needs queue occupancy, wait duration, and backend failure diagnostics.
Payload size still affects memory within the event count bound.

## Dependencies

[Capture failure handling](../../implemented/bug-fix/2026-09-08-puller-capture-failures.md)
preserves existing atomic event/token commits and terminal write failures;
[history-gap recovery](../architecture/2026-09-07-puller-history-gap-recovery.md)
handles upstream history expiry during prolonged stalls.
