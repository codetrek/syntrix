# Interfaces and Migration Plan

## Public Surface (Why/How)

- Why: give callers a tiny, stable API and hide transport/storage/HTTP details.
- How: expose only factory + engine interfaces; everything else lives in internal adapters.

### Interfaces (Go sketch)

```go
// TriggerEngine is what services use.
type TriggerEngine interface {
    LoadTriggers(triggers []*Trigger)
    Start(ctx context.Context) error
    Close() error
}

// TriggerFactory builds a TriggerEngine from config/deps.
type TriggerFactory interface {
    Engine() TriggerEngine
    Close() error
}
```

### Adapter Interfaces

- DocumentWatcher: `Watch(ctx) (<-chan storage.Event, error)` and `SaveCheckpoint(ctx, token any) error` hidden in engine.
- TaskPublisher: `Publish(ctx, *DeliveryTask) error` with NATS JetStream impl.
- TaskConsumer: `Start(ctx) error` with worker partitioning; owns stream/consumer lifecycle.
- DeliveryWorker: `ProcessTask(ctx, *DeliveryTask) error` HTTP caller with auth + signing.
- Evaluator: existing CEL evaluator reused as is but injected.

## Package Layout

- `internal/trigger/engine`: orchestration and exposed interfaces.
- `internal/trigger/internal/watcher`: storage watch + checkpoint adapter.
- `internal/trigger/internal/pubsub`: NATS publisher/consumer + config.
- `internal/trigger/internal/worker`: HTTP worker, auth/signature helpers.
- `internal/trigger/internal/config`: trigger config structs (NATS, HTTP, retry/backoff, pools).
- Keep existing types (Trigger, DeliveryTask, RetryPolicy, Duration) in `internal/trigger`.

## Migration Steps

1) Define `TriggerEngine` and `TriggerFactory` interfaces + factory struct wiring existing pieces.
2) Carve DocumentWatcher adapter from current `TriggerService.Watch` (checkpoint handling stays, but hidden).
3) Move NATS publisher/consumer into `internal/trigger/internal/pubsub` and expose via interfaces.
4) Keep evaluator logic; inject via factory.
5) Move DeliveryWorker to adapter package; keep behavior (auth token + signature) but make HTTP client configurable.
6) Update existing wiring code to use factory/engine, leaving public behavior unchanged.
7) Add unit tests for factory wiring, watcher checkpoint behavior, publisher/consumer error paths, worker HTTP success/failure.

## Testing Plan

- Use testify fakes/mocks for NATS JS, storage.DocumentStore, and HTTP server.
- Cover: resume token load/save, event dispatch to evaluator, publisher subject format, consumer partitioning, retry/backoff caps, worker header/signature + system token injection.
- Keep existing evaluator tests; extend with engine-level happy-path and failure-path cases.

## Open Questions

- Tenant isolation: keep single TRIGGERS stream with subject prefixes or split per tenant/collection? Impact on retention and quotas.
- Checkpoint location: continue using `sys/checkpoints/trigger_evaluator` or move to a dedicated collection/key (and whether to namespace per tenant).
- Observability: do we need metrics/tracing hooks in the public engine or adapters (NATS publish/consume, HTTP latency, evaluator errors)?
- Config surface: should retry/backoff defaults remain hardcoded or be part of trigger config and/or factory config?
