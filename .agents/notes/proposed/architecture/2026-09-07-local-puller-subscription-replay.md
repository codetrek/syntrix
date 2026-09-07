# Agent Note: Local Puller Subscription Replay

Status: proposed

## Problem

Standalone consumers receive a weaker recovery contract than remote consumers.
In [local Subscribe](../../../../internal/puller/core/puller.go#L535), the
`after` marker initializes subscriber progress, but the loop reads only
`case evt := <-sub.Events():`; it never requests historical events or handles
the overflow flag set by
[SubscriberManager](../../../../internal/puller/core/subscriber.go).
An invalid marker is also discarded when decoding fails. Static inspection
therefore identifies missing integration, not an absent replay implementation:
the [gRPC handler](../../../../internal/puller/grpc/server.go#L205) already calls
`s.eventSource.Replay(ctx, sub.CurrentProgress().Positions, sub.CoalesceOnCatchUp)`
and switches between catch-up and live delivery.

Sharing that loop also requires a correct ordering contract. The current
[buffer key](../../../../internal/puller/events/types.go#L124) uses
`FormatBufferKey(e.ClusterTime, e.EventID)`, and the
[event ID](../../../../internal/puller/normalizer/normalizer.go) includes a hash.
For equal timestamps, a later-ingested event can sort before an earlier event's
saved key. A timestamp/hash comparison cannot safely decide that it was already
delivered.

## Proposal

Give local and gRPC subscriptions one shared catch-up/live state machine, with
transport adapters responsible for delivery and errors. A nonempty valid marker
starts replay; an empty marker retains the protocol's current-head semantics.
Register live capture before opening replay, track progress per backend, and
recover channel overflow from the last successfully delivered position. Use the
durable monotonically increasing committed position assigned per backend by
[persist before publish](2026-09-07-puller-persist-before-publish.md) for buffer
order, progress markers, replay bounds, live delivery, and overlap deduplication.
Compare positions only within the same backend and continuity generation.
Preserve the stable upstream event identity separately; timestamps and event-ID
hashes are neither resume positions nor proof that an event was delivered.

Extend the local subscription contract to expose a terminal error separately
from normal cancellation, then update all local consumers. Malformed markers,
iterator failures, and unavailable history must reach the caller. The caller
continues to own the checkpoint saved after processing; successful transport
delivery is not a processing acknowledgement. Cancellation closes iterators and
removes exactly the subscription being canceled.

## Alternatives

**Route standalone consumers through gRPC.** This reuses the existing handler
but adds a required listener and serialization to the in-process deployment.
Sharing the state machine preserves the existing deployment distinction.

**Duplicate the gRPC loop locally.** This avoids extracting shared code, but
leaves future overflow and cursor fixes with two independent owners.

## Acceptance Criteria

- The same retained-history fixtures pass through local and gRPC adapters,
  including multiple backends and databases and no-history subscriptions.
- Two events with equal timestamps and reversed lexical event-ID/hash order
  retain ingestion order in both adapters. Saving progress after the first,
  then crashing and restarting, delivers the second through replay and a
  replay/live transition without skipping it.
- Overflow during replay and live delivery reaches the retained head without
  missing events or an advancing checkpoint for undelivered events.
- Invalid markers, replay failures, and expired history produce distinguishable
  terminal errors; cancellation releases subscriptions and iterators within a
  bounded test timeout.
- Restarting a consumer from its persisted processed checkpoint replays all
  subsequent retained events.

## Risks

The local interface change affects Indexer, Streamer, and Trigger callers.
Slow-consumer recovery costs disk reads and can exceed retention. Diagnose
mode changes and failures by consumer, backend, and event identifier without
logging document payloads or raw resume tokens.

## Dependencies

[Persist before publish](2026-09-07-puller-persist-before-publish.md) owns durable
position assignment and event identity;
[history-gap recovery](2026-09-07-puller-history-gap-recovery.md) defines the
terminal continuity failure;
[consumer settings](../bug-fix/2026-09-07-puller-admission-and-catch-up-settings.md)
owns admission and catch-up policy.
