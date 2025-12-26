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

1) DocumentWatcher reads resume token from storage, opens watch stream, emits events.
2) TriggerEngine invokes Evaluator per event, builds DeliveryTask when matched.
3) TaskPublisher pushes tasks to NATS `triggers.<tenant>.<collection>.<docKey>`.
4) TaskConsumer pulls from `TRIGGERS` stream, partitions by collection+docKey, dispatches to DeliveryWorker.
5) DeliveryWorker POSTs to target URL with headers, signature, and optional system token.
6) Checkpoint updated after each processed event (at-least-once, same key as today unless decided otherwise).

## ASCII Module Diagram

```
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

- TriggerFactory accepts trigger-specific config: NATS stream/consumer names, worker pool size, backoff defaults, HTTP timeouts, signature secret source placeholder.
- External services only pass config + dependencies; they do not call NATS/HTTP directly.

## Compatibility Notes

- Keep current subject scheme and retry/backoff math; future changes go through config gates.
- CEL condition semantics remain identical; collection matching still supports glob via `path.Match`.
