# Agent Note: Advance Trigger Checkpoints After Durable Scheduling

Status: proposed

## Problem

[The evaluator](../../../../internal/trigger/evaluator/service.go) now stops on
evaluation or publication failure and exposes only completed progress to its
asynchronous saver. Checkpoint-save failure stops ingestion, and source shutdown
cancels the saver before joining it. This closes the earlier continue-after-error
ordering hazard.

The current boundary is still publisher success. Partial fan-out followed by a
crash can replay tasks and evaluate against a changed rule set. Standalone memory
publication is not durable scheduling. Without persisted selection, outcomes,
and outbox records, a checkpoint cannot prove that every intended task remains
recoverable independently of broker or process lifetime.

## Proposal

Track the highest contiguous event position whose durable scheduling is complete.
Use the same boundary in standalone and distributed modes: an event becomes
checkpoint-eligible only after its rule selection and evaluation outcomes are
persisted, every matched task has a durable outbox record, and the scheduling
record is marked complete. An event matching no rules requires a persisted
successful no-match outcome. The idempotency proposal owns these records and
their recovery; this proposal owns advancement of source progress.

An evaluation failure, outbox write failure, or incomplete fan-out blocks
advancement across that event and enters a bounded, cancellable retry or explicit
operator-visible failure state. Recovery resumes against the persisted rule
selection and outcomes. It must not combine partial scheduling with a newly
loaded rule set.

Broker acknowledgement records dispatch progress only. Once durable scheduling
is complete, the checkpoint may advance while the broker is unavailable; the
outbox dispatcher retains responsibility for eventual publication and delivery.
Memory enqueue success and durable broker acknowledgement cannot independently
make an incompletely scheduled source event checkpoint-eligible.

Keep asynchronous checkpoint coalescing, but save only completed progress and
retry failed checkpoint writes without moving the completion boundary backwards.
Stop and join the saver when the source closes. On shutdown, use a bounded flush
context independent of the cancelled processing context. Required scheduling
storage and dispatcher dependencies must be validated during service assembly.

A crash before checkpoint persistence may replay an already scheduled event;
recovery must reuse its durable scheduling records. Previously skipped work
requires an explicit replay boundary chosen from retained source history.

## Alternatives

**Use broker acknowledgement as the checkpoint boundary** avoids durable
scheduling records but does not preserve rule selection during partial fan-out
and cannot provide the same recovery behavior for standalone memory queues.

**One transaction across source, checkpoint, and broker** would provide stronger
atomicity, but the current independent storage and broker interfaces do not share
such a transaction. Durable scheduling plus replay separates these boundaries.

## Acceptance Criteria

- If one matched task fails its outbox write or fan-out completion remains
  unrecorded, persisted progress never passes that source event.
- A broker outage permits progress after complete durable scheduling; restart
  and broker recovery deliver the retained tasks using their original identities.
- Crashes during rule selection, evaluation, fan-out, or checkpoint persistence
  reuse the recorded rule set and outcomes without silently omitting tasks.
- Evaluation and scheduling/checkpoint-store failures are observable and use
  bounded backoff and cancellation.
- Source closure and cancellation terminate checkpoint goroutines within the
  documented deadline; multiple databases preserve aggregate progress order.

## Risks

A scheduling failure can delay unrelated databases sharing one aggregate
checkpoint. Advancing during a broker outage grows the durable outbox, so its
admission bounds must propagate backpressure before storage is exhausted.

## Dependencies

[Delivery idempotency](../architecture/2026-09-07-trigger-delivery-idempotency.md)
owns durable rule selection, evaluation outcomes, task records, and dispatch.
[Local Puller replay](../../implemented/architecture/2026-09-07-local-puller-subscription-replay.md)
and [history-gap recovery](../../implemented/architecture/2026-09-07-puller-history-gap-recovery.md)
own the availability of recoverable source events.
