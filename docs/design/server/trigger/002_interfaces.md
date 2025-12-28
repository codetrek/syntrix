# Interfaces and Migration Plan

## Public Surface (Why/How)

- Why: give callers a tiny, stable API and hide transport/storage/HTTP details.
- How: expose only factory + engine interfaces; everything else lives in internal adapters.

**Tenant awareness:** `Trigger` and `DeliveryTask` require `Tenant`; publishing and consuming route via `triggers.<tenant>.>` and checkpoints are per-tenant keys.

### Tenant trust and safety constraints

- Tenant source: Trigger definitions are loaded in a tenant context; factory must validate the tenant in trigger config matches the scoped tenant passed to the factory. Reject or fail fast on mismatch.
- docKey safety: Subjects must not include `.` `*` `>` wildcards. Use a subject-safe encoding for `DocKey` (decision: base64url without padding). Publisher encodes before subject construction; consumer decodes before handing to the worker; enforce subject-length guard (1024-byte NATS limit) and hash docKey segment using SHA-256 hex truncated to 32 chars if the encoded subject would exceed the limit (payload keeps original docKey).
- Tenant/collection validation: enforce allowed charset (letters, digits, dash, underscore) and max length (default <=128 chars each) at factory load; fail fast on violation to avoid routing breakage.
- Secret/token isolation: system tokens are minted per tenant; secrets referenced by `SecretsRef` resolve within the tenant namespace. Cross-tenant secret reuse is forbidden.
- Watch scope: default watch all collections per tenant; optional include/exclude collection prefixes provided via factory config.
- Hash fallback observability: when hashing is used, mark `subjectHashed=true` in payload and metrics to aid collision/debug visibility.
- Validation failure semantics: any tenant/collection/name or docKey encoding violation is fail-fast at factory/load time; do not start partially.
- Secret/token failure handling: fetch/mint failures are treated as task failures and follow retry policy; no silent drop.
- Checkpoint/filter interaction: if collection include/exclude filters change, reset or re-seed checkpoints for affected collections; allow admin-provided resume token for controlled replay.

### Interfaces (Go sketch)

```go
// TriggerEngine is what services use.
// LoadTriggers MUST validate tenant/collection/docKey and return an error on any invalid trigger;
// callers should treat errors as startup-blocking to avoid partial activation.
type TriggerEngine interface {
    LoadTriggers(triggers []*Trigger) error
    Start(ctx context.Context) error
    Close() error
}

// TriggerFactory builds a TriggerEngine from config/deps.
type TriggerFactory interface {
    Engine() TriggerEngine
    Close() error
}

// NewFactory creates a new TriggerFactory.
// The implementation details (defaultTriggerFactory, defaultTriggerEngine) are unexported.
func NewFactory(store storage.DocumentStore, nats *nats.Conn, auth identity.AuthN, opts ...FactoryOption) TriggerFactory {
    // ...
}
```

### Adapter Interfaces

- DocumentWatcher: `Watch(ctx) (<-chan storage.Event, error)` and `SaveCheckpoint(ctx, token any) error` hidden in engine.
- TaskPublisher: `Publish(ctx, *DeliveryTask) error` with NATS JetStream impl.
- TaskConsumer: `Start(ctx) error` with worker partitioning; owns stream/consumer lifecycle.
- DeliveryWorker: `ProcessTask(ctx, *DeliveryTask) error` HTTP caller with auth + signing.
- Evaluator: existing CEL evaluator reused as is but injected.

Partitioning and hashing contracts

- Consumer partition key and idempotency key use payload `DocKey` (decoded), not the hashed subject fragment. Why: hash fallback alters the subject segment to fit length limits; using the hashed fragment can co-locate distinct docs and break ordering/dedupe guarantees.
- When subject hashing occurs, publisher sets `subjectHashed=true` in payload/metrics; consumer surfaces metrics for hash-path traffic and logs any detected collision. Why: hash path is exceptional and needs visibility for diagnosis.
- Hash collision handling (low priority): on detected collision, define consumer behavior (e.g., serialize, flag, and fail fast) and emit alerts. Why: deterministic handling prevents silent ordering/dedup errors.

Migration note (low priority)

- Callers of `LoadTriggers` must handle the returned error and fail startup/mark unhealthy on validation failures; do not ignore errors. Why: silent skips lead to partial trigger activation without visibility.

## Package Layout

- `internal/trigger/engine`: orchestration and expose interfaces.
- `internal/trigger/internal/watcher`: storage watch + checkpoint adapter.
- `internal/trigger/internal/pubsub`: NATS publisher/consumer + config.
- `internal/trigger/internal/worker`: HTTP worker, auth/signature helpers.
- `internal/trigger/internal/config`: trigger config structs (NATS, HTTP, retry/backoff, pools).
- Keep existing types (Trigger, DeliveryTask, RetryPolicy, Duration) in `internal/trigger`.

## Migration Steps

> **Moved to Task 010**: The detailed migration steps have been integrated into the "Plan / Milestones" section of `tasks/010.2025-12-28-trigger-engine-refactor.md`.

## Testing Plan

- Use testify fakes/mocks for NATS JS, storage.DocumentStore, and HTTP server.
- Cover: resume token load/save, event dispatch to evaluator, publisher subject format, consumer partitioning, retry/backoff caps, worker header/signature + system token injection.
- Cover: tenant mismatch rejection in factory/load path, docKey encoding/decoding invariants (published subject matches decoded payload), subject-length guard (hash fallback), hash-flag propagation, tenant/collection name validation failures, secret lookup within tenant namespace, and secret/token retrieval failure behavior (should follow retry policy or terminate accordingly).
- Keep existing evaluator tests; extend with engine-level happy-path and failure-path cases.

## Open Questions

- Tenant isolation: decided to keep a single `TRIGGERS` stream with `triggers.<tenant>.>` subjects; revisit only if quotas/retention require per-tenant streams.
- Checkpoint location: use tenant-scoped checkpoint keys (e.g., `sys/checkpoints/trigger_evaluator/<tenant>`) to avoid cross-tenant resume token sharing.
- Observability: optional for now; add hooks later if needed.
- Config surface: retry/backoff remains configurable via trigger config; stream/consumer names and tenant scope provided via factory config.
