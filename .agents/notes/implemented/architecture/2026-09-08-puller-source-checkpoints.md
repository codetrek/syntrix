# Agent Note: Puller Source Checkpoints

Status: implemented

## Problem

A capture restart needs to identify both its source and its exact native
position. A raw token alone cannot reject a changed collection incarnation or
capture scope. A local event key also cannot establish source progress, and a
cache-wide latest token cannot identify the position of every cached event.

Idle streams need a durable starting boundary. Event admission, asynchronous
cache commits, and reconnects must preserve the order of source positions:
otherwise a retry can reload an older disk checkpoint and later overwrite newer
progress. Clearing an unusable checkpoint would conceal missing history.

## Decision

The [Puller architecture](../../../../docs/design/server/puller/01.architecture.md#23-checkpoint-manager)
owns the capture contract. The
[checkpoint package](../../../../internal/puller/checkpoint/mongo.go) encodes a
versioned opaque native position with these bindings:

| Field | Ownership |
|---|---|
| Source ID | Stable configured backend name, shared by replicas of the same source |
| Physical source | Database name and sorted physical collection name/UUID pairs |
| Capture contract | Database-level `UpdateLookup` watch over a fixed allowlist, including source lifecycle controls |
| Position | Complete BSON `resumeAfter`, or exactly one explicit `startAt` timestamp |

The codec validates encoding, supported version and capture contract, source
identity, scope, and native token structure. It accepts at most 64 KiB per
encoded checkpoint, 32 KiB per native token, and 100 nested BSON levels.
Tokens remain opaque and unordered.
No endpoint, cache generation, event key, or local sequence defines checkpoint
validity. The physical collection UUID distinguishes a recreated source from a
replacement process or connection.

[Native capture](../../../../internal/puller/core/puller.go) retains one Mongo
database watch per backend. [Store Watch](2026-09-08-store-watch-contract.md)
selects a logical database/collection scope and has a separate codec; its frames
are not the input to this capture path. Before returning, the storage factory
prepares its [data/system namespaces](../../../../docs/design/server/core/storage/05.multi-database.md#2-document-identity-and-physical-namespaces)
on the document primary and explicit per-database backends, preserving existing
collection UUIDs. Read replicas and arbitrary external Puller scopes are outside
that preparation. Puller requires every allowlisted physical collection to
exist, reads source metadata before opening, and verifies it after opening.
A changed incarnation is rejected; Puller never creates a missing collection.

Fresh `from_now` capture opens with an empty first batch and saves its initial
server token before reading events. `from_beginning` saves the explicit `(1, 1)`
start timestamp and remains subject to Mongo's retained history. Resumed capture
validates the saved binding before applying its native position. Idle cursor
tokens advance progress only after earlier events have entered the same queue.
An event's checkpoint uses its raw token, never a later post-batch resume token.

The [buffer](../../../../internal/puller/buffer/buffer.go) has format version 1;
each [record](../../../../internal/puller/buffer/reader.go) contains its version,
event, and source checkpoint. `ReadRecord` and `Iterator.Checkpoint` expose that
association. Event admission remains asynchronous. Explicit initial/idle saves
wait for the shared ordered queue to commit; each batch atomically syncs its
event records and final source checkpoint. A process retains its latest admitted
checkpoint across transient retries, and startup loads the committed checkpoint.
Writer failures remain visible to subsequent operations and repeated `Close`.

[Recovery](../../../../internal/puller/recovery/recovery.go) stops capture on
malformed or incompatible checkpoints, source/scope mismatch, source lifecycle
invalidation, and unavailable native history. It preserves the failure cause
and never clears the checkpoint to restart at the current head. Error messages
exclude checkpoint/token contents. Transient connection failures may reconnect
from the retained admitted position.

## Alternatives

**Use cache generations and sequences as consumer progress.** This was rejected
because replacing disposable storage would invalidate a source-resumable
checkpoint and prevent failover between Pullers of the same source. Private
cache ordering may still need separate metadata, but it cannot define source
checkpoint validity.

**Route capture through DocumentStore.Watch.** That contract supplies scoped
document frames, while Puller captures the fixed physical collection set for a
backend. Unifying them requires a source inventory and scope/aggregation
decision. Keeping native capture preserves that boundary without expanding the
Store API or claiming the codecs are interchangeable.

**Migrate legacy cache data during open.** Existing records lack per-event source
checkpoints; their timestamp/hash keys cannot reconstruct native positions.
The cache format therefore rejects nonempty unversioned state. Replacement is
an explicit deployment action, with no inferred conversion or automatic reset.

**Deliver the consumer recovery redesign together.** This would also change
progress persistence, the gRPC protocol, replay/live coordination, and consumer
failure handling. Those remain separate work, with the structural costs below.

## Consequences

Capture restart now binds retained progress to the same native source and scope.
Capture requires metadata permission. Storage initialization also requires
collection-creation permission when its physical namespaces are missing. Source
history or incarnation loss stops the backend and requires an explicit recovery
decision. This does not provide a consumer-visible recovery protocol.

The cache upgrade is destructive: old nonempty caches must be deliberately
replaced before startup. Old events and their local replay positions are then
unavailable; operators must account for consumer recovery and the configured
bootstrap boundary. The cache marker is a format version, not a continuity
generation or a new source authority.

Consumer `ProgressMarker` remains a backend-to-EventID map and the gRPC wire
format remains unchanged. Publication and replay may still expose admitted
events before commit. Per-record source checkpoints provide the required input
for later recovery work; they do not establish reliable end-to-end delivery.

| Deferred work | Cost and constraint |
|---|---|
| Consumer source positions and received/processed client state (draft) | Define wire/persisted source progress and reconnect fencing. Transport reconnect resumes admitted position `R` while its FIFO remains intact; whole session loss resumes the owner's durable processed position `C`. The full consumer algorithm remains deferred |
| [Local/gRPC replay](../../proposed/architecture/2026-09-07-local-puller-subscription-replay.md) and [history-gap recovery](../../proposed/architecture/2026-09-07-puller-history-gap-recovery.md) | Add native replay on cache miss, replay/live coordination, and equivalent terminal errors; cache absence cannot invalidate a retained source checkpoint |
| [Cache publication and ordering](../../proposed/architecture/2026-09-07-puller-persist-before-publish.md) | Revisit timestamp/hash navigation and delivery timing; a private cache sequence must remain separate from source progress |
| [Before-images](../../proposed/feature/2026-09-07-trigger-before-images.md) | Define retained event-time payloads and consumer transport; `UpdateLookup` does not establish historical images |
| 100-events/10ms cache target | Change and validate batching separately; this checkpoint change preserves existing batch settings |

This decision partially supersedes the three linked Puller proposals only where
they assign consumer checkpoint authority to local generation/sequence state.
Their remaining cache, replay, and consumer recovery work stays proposed.

The accompanying tests cover codec portability and malformed input, metadata
and incarnation mismatch, initial/idle/event ordering, admitted-position retry,
terminal native failures, cache reopen and format rejection, and sticky batch
failure/close results. Codec and mocked-cursor tests do not establish production
Mongo retention behavior or consumer failover correctness.
