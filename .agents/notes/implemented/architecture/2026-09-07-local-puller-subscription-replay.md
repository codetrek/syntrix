# Agent Note: Local Puller Subscription Replay

Status: implemented

## Problem

The former local subscription path accepted a saved marker but delivered only
new events. It discarded invalid markers and did not recover its overflow flag,
while gRPC owned a separate catch-up loop. Standalone consumers therefore had a
weaker recovery contract. Timestamp/hash comparisons also failed to establish a
safe replay/live boundary for equal-timestamp events.

## Decision

The [core subscription](../../../../internal/puller/core/subscriber.go) owns
one shared state machine for local and gRPC callers. Registration and publication
share a coordinator: each subscription captures a cut, replays through it, and
queues newly committed records after it. Real-time delivery stays in memory;
catch-up reads bounded safe Pebble pages. Overflow captures a new cut and replays
from the last delivered position without blocking the committer on transport.

`Subscribe(ctx, options)` returns `Subscription` and an error. `Next(ctx)` returns
an event/progress envelope or terminal error, and `Close` is idempotent. The first
envelope contains the effective starting progress without a change. Empty
`After` starts at published heads; supplied cursors must validate every source,
generation, and retained position. Unique internal registrations keep duplicate
diagnostic consumer labels independent.

Both paths enforce count/byte queues, bounded replay pages, and active
subscription admission. Coalescing requires server permission and caller opt-in;
the configured threshold selects it during replay. A coalesced window advances
progress only after all output is delivered, and preserves a terminal delete so
partial-window replay converges. gRPC carries the initial start in readiness
headers and maps domain failures through `ErrorInfo` without new protobuf fields.

Direct consumers preserve progress-only envelopes and stop on unresolved
processing/upstream failures. Indexer fences checkpoint writes after asynchronous
store failure. Streamer closes its owned downstream streams on upstream failure.
Trigger cancels its checkpoint saver before joining it when its source ends.

## Alternatives

**Route standalone through gRPC.** This reused a handler but required a listener
and serialization in the direct-call deployment.

**Duplicate the gRPC loop locally.** This left cursor, overflow, and error fixes
with separate owners and different failure opportunities.

## Consequences

The local interface changes all direct consumer loops. Successful `Next` means
delivery, not completion of the caller's side effects. Saving a processed
checkpoint remains the consumer's responsibility; duplicates remain possible.
Slow consumers pay replay I/O and can receive an explicit history-expired error.

Proactive lag-triggered switching before queue overflow remains in the
[admission and catch-up proposal](../../proposed/bug-fix/2026-09-07-puller-admission-and-catch-up-settings.md).
The [subscription design](../../../../docs/design/server/puller/02.adaptive-consumption.md)
owns exact handoff and policy semantics. Durable publication and retention are
owned by [publication](2026-09-07-puller-persist-before-publish.md) and
[continuity](2026-09-07-puller-history-gap-recovery.md).
