# Agent Note: Complete the Indexer Recovery Lifecycle

Status: proposed

## Problem

The [Indexer service](../../../../internal/indexer/service.go) loads templates and saved progress, then subscribes to events. Its production construction path does not instantiate the existing [reconciler](../../../../internal/indexer/reconciler/reconciler.go) or [rebuild orchestrator](../../../../internal/indexer/rebuild/rebuild.go), and no production caller supplies their storage scanner and event replayer. Existing documents, newly configured indexes, and a lost memory index therefore lack a complete recovery path. This is an integration finding from static call-site inspection, not a restart test result.

The [storage design](../../../../docs/design/server/indexer/04.storage.md) requires memory reconstruction and persistent-index checkpoint recovery. These requirements cannot be satisfied by processing only subsequent changes.

## Proposal

Make the Indexer service own reconciliation, rebuild jobs, and their shutdown. Supply storage-scanning and Puller-replay adapters in both deployment modes. Discover desired database/collection indexes from configuration and storage metadata, including collections with no recent events.

For each rebuild, capture a durable replay position before scanning, scan with stable pagination, apply replay through a known boundary, and switch to live consumption without a gap. Apply one document-ID, pattern-matching, ordering-key, and tombstone policy across scan, replay, and live updates. Fence obsolete jobs when templates change. Mark an index ready only after every required write and checkpoint succeeds; persist enough generation and state information to resume safely or deliberately restart reconstruction.

Bound scan batches, concurrent jobs, and replay backlog. Cancellation must release iterators and stop owned workers before the store closes. Preserve the [Query integration](../../../../docs/design/server/query/02.indexer-integration.md) behavior of explicit index-unavailable errors during rebuilding. Emit database, template, job generation, progress counts, and failure cause without document bodies.

## Alternatives

**Rebuild every index on every start.** This simplifies checkpoint validation but imposes full scans and query downtime even when persistent data is intact. Retain full rebuild as recovery from invalid state.

**Only replay retained events.** This avoids scanning, but cannot reconstruct documents older than retained history or guarantee a complete newly added index.

## Acceptance Criteria

- Pre-existing documents appear after initial startup and memory-index restart in both deployment modes.
- Concurrent writes, deletes, and duplicate replay converge to storage state across multiple databases and matching collection patterns.
- Persistent restart, missing history, template replacement, scan/write failure, and cancellation never expose a partial index as ready.
- Configured scan and concurrency limits hold, and shutdown leaves no owned workers running.

## Risks

Long scans may outlive retained replay history and require a visible retry or failure. Rebuilds consume storage capacity and temporarily remove affected query availability. Recovery metadata requires an explicit migration or index rebuild when its format changes.

## Dependencies

[History-gap recovery](2026-09-07-puller-history-gap-recovery.md), [local replay](2026-09-07-local-puller-subscription-replay.md), and [query cursor pagination](../feature/2026-09-07-query-cursor-pagination.md) track the proposed recovery boundaries and scan traversal. The [publication proposal](../../rejected/architecture/2026-09-07-puller-persist-before-publish.md) is rejected; a usable replay boundary remains an unresolved prerequisite. [Management RPCs](../feature/2026-09-07-indexer-management-rpcs.md) expose this lifecycle.
