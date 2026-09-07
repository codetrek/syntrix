# Agent Note: Bound Puller Pending Writes

Status: implemented

## Problem

The former pending-write slice grew independently of configured queue capacity.
A blocked disk commit could retain an unbounded number of payloads. Limiting
only the waiting queue would still omit records already removed for batching
or synchronous submission.

## Decision

The [buffer writer](../../../../internal/puller/buffer/writer.go) charges
per-backend count and encoded-byte credits from admission through commit and
publication callback completion. `queue_size` defaults to 10000 and
`queue_bytes` to 67108864. Waiting producers are cancellable and receive the
writer's terminal failure or closure. Encoding is serialized with admission so
blocked producers cannot accumulate frozen payloads outside the queue budget.

`batch_size` and `batch_bytes` are strict batch maxima, defaulting to 100 and
16777216. The batching deadline is the oldest admitted event's time plus
`batch_interval`, default 100ms; later arrivals cannot postpone it. An individual
record exceeding the batch or queue byte budget fails explicitly. Encoded
identity and key metadata are included in write credits.

Close stops admission and drains accepted records. If the caller's deadline
expires during physical Sync, the writer retains the DB and batch until that
operation finishes. Cancellation cannot truthfully promise that an admitted
record was never committed.

## Alternatives

**Return overload whenever the queue is full.** This bounds admission but moves
retry ownership into ingestion. Blocking admission directly backpressures the
source, while an oversized record still fails explicitly.

**Use only a buffered channel.** Its capacity excludes the in-flight batch and
cannot cover payloads frozen before entering the channel.

**Limit only event count.** This was the initial proposal's budget. The delivered
scope also limits bytes because event-size variance otherwise leaves a large,
operator-uncontrolled memory range within the event-count bound.

## Consequences

Storage stalls pause ingestion and can outlast MongoDB history retention. The
[continuity decision](../architecture/2026-09-07-puller-history-gap-recovery.md)
turns unavailable history into an explicit failure. Backends maintain independent
write budgets and ordering.

These are encoded-work budgets, not total-process RSS limits. Driver buffers,
temporary encoding, Pebble caches, live subscriber queues, and replay pages
have separate costs. [Durable publication](../architecture/2026-09-07-puller-persist-before-publish.md)
defines the completion point; [configuration](../../../../docs/design/server/puller/01.architecture.md#configuration-and-latency)
documents defaults and trade-offs.
