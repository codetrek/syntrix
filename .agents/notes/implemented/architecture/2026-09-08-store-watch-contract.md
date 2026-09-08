# Agent Note: Store Watch Contract

Status: implemented

## Problem

A caller that consumes a document change stream needs to distinguish document
identity, source-change identity, and processed progress. A channel of events
with an untyped resume token does not establish a durable encoding, an initial
position on an idle source, ordered progress past filtered events, or a place to
observe failures after the channel opens.

Shared physical collections and database routing add another requirement: a
continuation must not be accepted for a different logical scope or replaced
source. Ordinary read routing also need not select the authoritative change
source. These ambiguities make it possible to report a resumed stream whose
history or scope differs from the caller's saved state.

## Decision

The [DocumentStore API](../../../../internal/core/storage/types/types.go) exposes
`Watch(ctx, database, collection, after, opts) (WatchStream, error)`. Its
[shared Watch types](../../../../internal/core/storage/types/watch.go) carry an
opaque string `WatchCheckpoint`, an initial checkpoint, ordered
`WatchFrame{Event, Checkpoint}` results, and terminal errors. The
[Store design](../../../../docs/design/server/core/storage/03.stores.md#2-document-watch)
owns the complete consumer contract.

### Scope, identity, and progress

A watch requires a logical database. An empty collection selects ordinary
logical collections in the selected data namespace; system collections require
an explicit selection. One call selects one source through the existing
routing facade. It does not enumerate logical databases or aggregate backends.

A fresh watch establishes a nonempty current checkpoint before returning,
including on an idle source. A resumed watch confirms exactly the requested
checkpoint. Each successful frame carries a checkpoint for a completed source
prefix. A nil event is progress without a document change. A consumer persists
the checkpoint only after all required work through that frame succeeds.
Checkpoints are opaque to consumers and cannot be ordered by their byte values.

`Event.Id` preserves its existing meaning as the document storage key.
`Event.ChangeID` identifies one source change stably across watches and retries.
A change identity is independent of the consumer's processed checkpoint.
Backend-native token fields are absent from the shared Event type.

The Mongo implementation's private versioned envelope binds the physical
namespace and collection UUID, logical database, collection selection,
`IncludeBefore`, and complete BSON resume token. It validates the encoding
strictly. Source identity comes from the Mongo collection, so replacing the
Store, client, or Puller does not invalidate a retained continuation. A new
physical collection incarnation does invalidate it. Native event tokens mark
event boundaries; idle native-cursor tokens provide ordered progress without
requiring another document change. This keeps a batch's later resume position
from being saved before its last event is processed.

### Opening, routing, and failures

Mongo Watch requires distinct physical data and system collection names. An
identical mapping returns `WatchUnsupported` before opening a cursor: a shared
namespace would allow an ordinary-collections watch to include system records.
This guard belongs to Watch and leaves CRUD configuration handling unchanged.

Mongo reads the collection UUID before opening a cursor and verifies it again
after opening. A fresh watch creates a missing selected physical collection to
establish its identity and an initial server position. It surfaces metadata,
change-stream, and collection-creation permission errors. A resumed missing or
replaced source returns `WatchSourceMismatch`.

The [routing facade](../../../../internal/core/storage/router/routed_store.go)
uses `OpWatch`. The [split router](../../../../internal/core/storage/router/split.go)
sends this operation to the primary while ordinary `OpRead` operations retain
the replica. Source/scope mismatches and unavailable history propagate to the
caller; Store and router never discard the checkpoint and open a fresh stream
implicitly.

`WatchError` exposes scope, checkpoint, source, history, payload, malformed-event,
capability, permission, and availability categories while preserving the
original error chain. Decode failures and native cursor errors terminate the
stream. Cancellation of the watch or a read is terminal. Callers serialize
reads; closing a stream can interrupt a blocked read, is idempotent, and gives
Mongo cursor cleanup a five-second timeout. Cleanup failures are reported, and
other watches and Store operations retain their shared connection.

### Payload and collection routing

The existing document key `database:hash(fullpath)` remains unchanged. The
prefix establishes database identity even on a physical delete, but its hash
cannot recover a logical collection path. Mongo requests available source
pre-images for specific-collection routing even when `IncludeBefore` is false.
If neither source pre-image nor current enrichment can establish membership,
the stream returns `WatchPayloadUnavailable`. It neither leaks an event from
another collection nor skips an event whose membership is unknown.

An ordinary-collections watch can emit a physical delete using document identity
alone. The current Mongo Watch adapter emits `EventDelete` for both the logical
tombstone transition and physical removal, with `Document == nil` for either
event. An update's immutable `updateDescription` identifies the tombstone
transition; source operations other than physical deletes still require
available document enrichment. `Document` on create/update events may reflect a
later lookup state. `Before` is exposed only when requested and available from
the source. This API does not promise retained historical payloads or enable
Mongo pre-images. Source capability and retention remain operational
prerequisites for a scope that needs those images or routing metadata.

The [document deletion lifecycle](../../../../docs/design/server/core/storage/03.stores.md#document-deletion-and-physical-cleanup)
owns the distinction between normal document deletion, physical cleanup, and
administrative purge. Normal `Delete` uses Mongo `UpdateOne` to retain a tombstone
with `deleted = true`, empty `data`, and identity/routing metadata. Subsequent
physical cleanup is garbage collection. Store Watch reports both source changes;
its shared event type does not identify which one was the logical business
deletion. This is separate from Puller's business conversion, which ignores raw
physical deletes and retains the available tombstone for a logical delete.

### Implementation evidence and adoption limits

The design documents own architecture contracts. This table records the
implementation evidence and limits relevant to applying those contracts.

| Boundary | Observed behavior and consequence | Evidence |
|---|---|---|
| Logical document deletion | `Delete` uses an update to set `deleted=true`, clear `data`, increment version, update time, and assign expiry. Identity and routing metadata remain. A missing or already deleted target returns `ErrNotFound`; a failed predicate on a live target returns `ErrPreconditionFailed`. | [Mongo document Store](../../../../internal/core/storage/mongo/document_store.go) |
| Recreation and administrative purge | Creating at a retained tombstone replaces it with the new document. Database purge physically removes data and system records, including live records, without writing per-document tombstones. | [Mongo document Store](../../../../internal/core/storage/mongo/document_store.go) |
| Cleanup prerequisites | Index initialization installs `sys_expires_at` TTL on the ordinary data collection, not the separate system collection. An expiry field alone does not guarantee system-record cleanup. | [Mongo document Store](../../../../internal/core/storage/mongo/document_store.go) |
| Raw Puller capture | The native pipeline filters configured physical collections through `ns.coll`; it retains physical delete operations. Logical database identity is derived from the available full document and can be absent on physical deletes. | [Ingestion](../../../../internal/puller/core/puller.go), [normalizer](../../../../internal/puller/normalizer/normalizer.go) |
| Business conversion | Physical `StoreOperationDelete` returns `ErrDeleteOPIgnored`. Update/replace with a tombstone becomes business delete with the document retained; live replacement becomes create, and live update remains update. Raw insert becomes create, including when coalescing supplied a tombstone. No previous image is populated. | [Transformation](../../../../internal/puller/events/transform.go) |
| Lookup and normalization failure | Puller classifies supplied latest-state images. Missing update/replace full documents are rejected, then logged and skipped by ingestion; this does not establish preservation of every logical transition. | [Normalizer](../../../../internal/puller/normalizer/normalizer.go), [ingestion](../../../../internal/puller/core/puller.go) |
| Raw coalescing | Insert followed by update retains the insert operation and latest payload; insert followed by physical delete cancels. These reductions do not certify preservation of each business transition. | [Coalescer](../../../../internal/puller/buffer/coalescer.go) |
| Store Watch classification | Logical and physical deletion both produce `EventDelete` without `Document`. Immutable deleted-field updates identify the logical transition, but enrichment remains required for non-physical-delete source events. Scoped routing may require pre-images even when `IncludeBefore` is false. Native cursor cleanup has a five-second deadline. | [Mongo Watch](../../../../internal/core/storage/mongo/watch.go) |
| Index application | A tombstone removes its index entry when `IncludeDeleted` is false; otherwise the supplied tombstone is upserted. Missing full documents are skipped. `ShowDeleted` requires a template retaining tombstones; physical cleanup provides no index invalidation guarantee. | [Indexer service](../../../../internal/indexer/service.go), [template selection](../../../../internal/indexer/manager/manager.go) |
| Streamer delivery | Business conversion precedes matching; physical deletes are ignored. Flattening supplies public document metadata when the document reference is available. The ingestion loop advances in-memory progress after received events, including ignored physical deletes. | [Streamer](../../../../internal/streamer/service.go), [flattening](../../../../internal/helper/convert.go) |
| Trigger evaluation | CEL exposes one `event` map. Document business fields are flattened, then stored `id`, `collection`, and `version` overwrite matching keys. A tombstone therefore supplies a metadata map, not previous business fields; the production path has no `Before`. Evaluation errors are logged and skip the affected rule. | [CEL context](../../../../internal/trigger/evaluator/cel/evaluator.go), [trigger service](../../../../internal/trigger/evaluator/service.go) |
| Trigger delivery | Task construction copies the tombstone's empty business map into `Payload`; JSON `omitempty` omits that field. This task representation is distinct from the CEL metadata map. | [Task construction](../../../../internal/trigger/evaluator/service.go), [delivery task](../../../../internal/trigger/types/types.go) |

MongoDB's [UpdateLookup contract](https://www.mongodb.com/docs/manual/changeStreams/#lookup-full-document-for-update-operations)
permits a later majority-committed document state rather than an event-time
image. The event delta and the looked-up document therefore have different
roles. Historical-image capture and end-to-end recovery remain separately
proposed work; the observations above do not make those guarantees implemented.

## Alternatives

**Keep the event channel and untyped raw tokens.** This retains the earlier call
shape but provides no Store-owned durable codec, idle initial anchor, ordered
progress-only result, or terminal read error. Adding these as unrelated callbacks
would divide subscription ownership and processing order across interfaces.

**Identify the source by a Store, client, or Puller instance.** This avoids source
metadata reads, but replacing a process or connection would reject valid retained
history. The physical collection UUID survives those replacements and changes
when the collection is recreated.

**Add a Store-owned durable change journal.** A journal could own replay retention
and richer event records, but requires write-path atomicity, retention, recovery,
and operational ownership across CRUD operations. The selected implementation
uses Mongo's retained source history and reports its limits. No independent
Store journal is introduced.

**Change document keys to encode logical collections.** This could identify a
collection from a hard-delete key, but would change existing document identity
and every CRUD key derivation. Source metadata supplies routing when available;
missing metadata is an explicit error under the preserved key scheme.

**Integrate the whole Puller pipeline in the same change.** Doing so would combine
a source API change with buffer durability, progress migration, local/gRPC
replay, and consumer recovery decisions. Those have separate proposal owners
and validation obligations. This decision delivers the Store source contract
while making the remaining integration costs explicit.

## Consequences

Callers can persist an opaque continuation, restart against the same retained
source, and distinguish event processing from source progress. The returned
change identity supports deduplication without reusing the document key or
claiming exactly-once processing. Primary watch routing does not make replica
CRUD reads current with a received event.

The API is a deliberate source-level change from the former channel and raw
`interface{}` token. That API had no public durable token codec. New checkpoints
use strict scope/source binding without runtime legacy inference. Callers with
persisted raw tokens require an explicit migration or a deliberate new starting
boundary that accounts for omitted history. Existing document keys and their
public `Event.Id` meaning remain intact.

Deployments that map ordinary data and system records to the same physical
collection cannot use Mongo Watch; opening a watch fails explicitly without
changing CRUD behavior. Fresh watches may create an empty selected physical
namespace, requiring additional collection-creation permission. Existing
watches also need metadata
access to obtain the UUID. Scope isolation can stop a specific-collection watch
when a physical delete or missing lookup lacks routing metadata, even if the
caller did not request `Before`. Operators must provide source history and
capabilities appropriate to their selected watch scope. Optional images do not
guarantee historical snapshots.

### Deferred integration and its costs

This note owns the implemented Store API, Mongo adapter, routing, and associated
validation. Puller ingestion still obtains Mongo clients through
[`StorageFactory.GetMongoClient`](../../../../internal/services/manager_init.go)
and bypasses `DocumentStore.Watch`. Its ingestion, batching, caches, pending
writes, persisted buffers, and local/gRPC delivery are unchanged by this
contract. The broader Puller design remains separate proposed work.

Puller's current raw `StoreChangeEvent` preserves Mongo operation types,
including physical deletes. The
[`events.Transform` converter](../../../../internal/puller/events/transform.go)
returns `ErrDeleteOPIgnored` for those deletes and maps update/replace events
with an available tombstone to business `EventDelete`, retaining that document.
It neither consumes Store Watch events nor supplies a historical before-image.
An update lookup can observe newer state; missing update/replace payloads are
rejected by the normalizer and logged/skipped by current ingestion. These limits
remain distinct from Store Watch's terminal payload failures and optional
source pre-images.

| Deferred work and owner | Cost and constraint retained here |
|---|---|
| Puller ingestion through the Store, described by the [Puller architecture](../../../../docs/design/server/puller/01.architecture.md) | Requires replacing direct Mongo watcher wiring and mapping Store frames/errors into ingestion; source codecs and native types must remain inside Store implementations |
| Existing [Puller cache publication](../../proposed/architecture/2026-09-07-puller-persist-before-publish.md) and [admission](../../proposed/bug-fix/2026-09-07-puller-pending-write-bound.md) proposals | Changes to caching, batching, and admission require a separately confirmed Puller design; Store checkpoints must remain portable and source-owned, and cache completion cannot define consumer checkpoint validity |
| [Local replay](../../proposed/architecture/2026-09-07-local-puller-subscription-replay.md) and [history-gap recovery](../../proposed/architecture/2026-09-07-puller-history-gap-recovery.md) | Requires durable continuity and retention boundaries plus consumer-visible failures; Store errors provide a source failure without implementing Puller generations or recovery |
| Multi-source discovery and aggregation in Puller, and [dedicated read/write routing](../../proposed/architecture/2026-09-07-dedicated-backend-read-write-routing.md) | Requires source inventory, ownership, and progress aggregation; one Store watch remains explicitly scoped, and separate source checkpoints must not be compared or combined as a scalar |
| [Indexer rebuild](../../proposed/architecture/2026-09-07-indexer-recovery-lifecycle.md) and [filtered subscription snapshots](../../proposed/bug-fix/2026-09-07-realtime-filtered-snapshots.md) | Requires scan/replay boundaries and consumer recovery integration; an initial Store checkpoint is not a snapshot or a rebuild mechanism |
| [Trigger before-images](../../proposed/feature/2026-09-07-trigger-before-images.md) and [delivery idempotency](../../proposed/architecture/2026-09-07-trigger-delivery-idempotency.md) | Requires retained payloads, transport changes, durable delivery/outbox decisions, and consumer state; optional Store images and ChangeID do not provide those guarantees |
| [Streamer durable progress](../../proposed/architecture/2026-09-07-streamer-durable-progress.md) and [SDK offline replication](../../proposed/feature/2026-09-07-sdk-offline-replication.md) | Requires progress persistence and delivery/acknowledgment policy in those consumers; receiving or closing a Store frame is not an end-client acknowledgment |

These gaps can still expose current Puller consumers to the failures described
by their proposal owners. Their statuses remain proposed; this implementation
does not establish a consolidated Puller redesign or an end-to-end durability
guarantee.
