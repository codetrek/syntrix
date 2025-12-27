# Trigger Architecture

## Overview (Why/How)

- Why: decouple trigger orchestration from transport/storage details and align with storage factory pattern for easier swaps and testing.
- How: introduce a `TriggerFactory` that wires adapters (storage watcher, evaluator, publisher, consumer, worker) into a single `TriggerEngine` interface used by services.

## Component Boundaries

- TriggerEngine (public): `LoadTriggers([]*Trigger)`, `Start(ctx)`, `Close()`. No direct NATS/HTTP/storage exposure.
- TriggerFactory (public): builds engine from config and injected dependencies (storage.DocumentStore, identity.AuthN, NATS conn, HTTP client options).
- Adapters (internal):
  - DocumentWatcher: wraps storage watch + checkpoint persistence.
  - Evaluator: CEL-based filter, cached programs.
  - TaskPublisher: NATS JetStream publisher, subject policy unchanged.
  - TaskConsumer: JetStream consumer + worker pool with partitioning by doc key.
  - DeliveryWorker: HTTP caller with auth token + signature helper.

## Data Flow

1) DocumentWatcher reads a tenant-scoped resume token from storage, opens the tenant watch stream, emits events.
2) TriggerEngine invokes Evaluator per event, builds DeliveryTask when matched.
3) TaskPublisher pushes tasks to NATS `triggers.<tenant>.<collection>.<docKey>` (tenant prefix enforces isolation on routing).
4) TaskConsumer pulls from the single `TRIGGERS` stream filtered by `triggers.>`, partitions by collection+docKey, dispatches to DeliveryWorker.
5) DeliveryWorker POSTs to target URL with headers, signature, and optional system token.
6) Checkpoint updated after each processed event using tenant-scoped keys to maintain per-tenant resume tokens (at-least-once semantics).
7) Watch scope: default watch all collections within a tenant; optionally restrict via include/exclude collection prefixes in config to reduce noise.
8) Checkpoint keys: `sys/checkpoints/trigger_evaluator/<tenant>`; if collection filtering is enabled, append the collection key to isolate per-collection progress.

## ASCII Module Diagram

```text
+----------------------+      +-------------------+      +--------------------+
| storage.DocumentStore|----->| DocumentWatcher   |----->| TriggerEngine      |
|  (watch/checkpoint)  |      | (resume token)    |      | (evaluate+dispatch)|
+----------------------+      +-------------------+      +---------+----------+
                                                                  |
                                                                  v
                                                        +-----------------+
                                                        | TaskPublisher   | -> NATS JetStream (TRIGGERS)
                                                        +-----------------+
                                                                  |
                                                                  v
                                                        +-----------------+
                                                        | TaskConsumer    | -- partitions --> Worker Pool
                                                        +-----------------+             |
                                                                                         v
                                                                                +----------------+
                                                                                | DeliveryWorker |
                                                                                | (HTTP+signing) |
                                                                                +----------------+
```

## Configuration Surfaces

- TriggerFactory accepts trigger-specific config: NATS stream/consumer names, worker pool size, backoff defaults, HTTP timeouts, signature secret source placeholder, tenant ID for watch scope and checkpoint key prefix, optional collection include/exclude lists, subject-length guard (hashing toggle) and docKey encoding strategy (base64url without padding), tenant/collection naming and length validation knobs, and hash-flag emission to payload/metrics.
- External services only pass config + dependencies; they do not call NATS/HTTP directly.

## Compatibility Notes

- Keep current subject scheme (`triggers.<tenant>.>` on a shared `TRIGGERS` stream) and retry/backoff math; future changes go through config gates.
- docKey inserted into subjects must use a subject-safe encoding (no `.` `*` `>` wildcards); publisher/consumer must apply the same encoding/decoding path.
- CEL condition semantics remain identical; collection matching still supports glob via `path.Match`.
- Single-stream assumption: monitor per-tenant traffic and retention; if quotas or noisy-neighbor risk are observed, pivot to per-tenant streams via config.
- Observability: emit per-tenant metrics for publish/consume/worker success, retry counts, and latency to detect tenant-specific regressions.
- Subject-length guard: enforce NATS subject length; hash docKey segment when encoded subject would exceed limit, keeping the original docKey in payload.
- Tenant/collection naming constraints: validate against allowed charset and length (configurable, default <=128 chars each) to prevent subject overflow and routing issues.
- Secret/token failure handling: if secret fetch or token minting fails, treat as task failure and follow retry policy; do not silently drop.
- Recovery semantics: on missing/cleared checkpoint, start from “now” (no historical replay); allow admin override to resume from a provided token for controlled replay.
