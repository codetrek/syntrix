# Trigger Evaluator Service

The evaluator consumes Puller changes, evaluates configured CEL rules, and
publishes matching delivery tasks. The watcher owns source subscription and
checkpoint storage; the service owns sequential processing and the checkpoint
writer's lifetime. Delivery workers independently execute Webhooks.

```text
Puller -> Watcher -> CEL evaluation -> Task publisher -> PubSub delivery queue
                         |
                         | entire event processed successfully
                         v
                 Completed progress -> checkpoint writer -> DocumentStore
```

## Public service

```go
type Service interface {
    LoadTriggers(triggers []*types.Trigger) error
    Start(ctx context.Context) error
    Close() error
}
```

`LoadTriggers` validates the complete replacement rule set. `Start` blocks until
cancellation or the first subscription, transformation, evaluation, publication,
or checkpoint failure. `Close` closes owned watcher and publisher resources.

## Components and contracts

`DocumentWatcher.Watch(ctx)` returns a `WatcherStream`; `Next(ctx)` returns a
transformed event or an error, and `Close` releases that subscription. It receives
all logical databases and preserves Puller's aggregate progress. A progress-only
envelope carries no document but still completes a source prefix. Terminal
subscription or transformation errors remain observable by the evaluator.

The CEL evaluator selects a trigger's database, operation, and collection before
checking its expression. The service builds and publishes every matched task
before advancing completed progress. If any evaluation or publication fails,
that source event cannot advance the checkpoint.

The asynchronous checkpoint writer stores only completed progress. A save failure
cancels the owned processing context. When upstream ends, that context is canceled
before the service joins the saver, so source failure cannot strand shutdown.
See [checkpoint design](01.checkpoint.md) for storage shape, bootstrap behavior,
replay duplicates, and the remaining durable-outbox guarantee.

`TaskPublisher` routes through the configured PubSub implementation: in-memory
for standalone or NATS for distributed deployment. Subject construction and
broker publication are described in [publisher design](03.publisher.md).

## Sources and related decisions

- [Factory and dependencies](../../../../../internal/trigger/evaluator/factory.go)
- [Watcher contracts](../../../../../internal/trigger/evaluator/watcher/interfaces.go)
- [Evaluator implementation](../../../../../internal/trigger/evaluator/service.go)
- [CEL evaluator](02.cel_evaluator.md)
- [Puller subscriptions](../../puller/02.adaptive-consumption.md)
