# Agent Note: Persist Trigger Tasks Before Broker Acknowledgement

Status: proposed

## Problem

Broker acknowledgement timing includes local queue waiting and HTTP execution.
A message can be redelivered while its task is still waiting or running. A local
active-task set cannot prevent another delivery replica from executing the copy.
The [execution timeout](../../implemented/bug-fix/2026-09-07-trigger-rule-timeouts.md)
correctly starts when execution begins; changing that boundary does not resolve
broker acknowledgement expiry or provide cross-replica task ownership.

## Proposal

Draft direction: transfer responsibility from NATS to durable task storage before
acknowledging receipt. Execution then uses the shared task state independently of
the broker delivery's lifetime. The runtime still acknowledges after processing;
this handoff and database-driven execution are not implemented.

```text
NATS delivery
    |
    v
Atomically accept or verify the durable task
    |                                |
    v                                v
Ack NATS                      Recoverable task state
                                     |
                              Atomic worker claim
                                     |
                              Begin attempt timeout
                                     |
                              HTTP -> Persist outcome
                                     |
                         Complete / Retry when eligible
```

### One Authoritative Task Record

The [delivery idempotency proposal](2026-09-07-trigger-delivery-idempotency.md)
owns stable delivery identity, durable task/outbox records, worker claims,
recovery, and retention. This proposal owns the broker-to-task-store handoff and
uses that same record. An upstream-created task may already exist: receipt must
establish or verify durable execution eligibility without creating a second
inbox or resetting its state.

| Receipt condition | Required outcome |
|---|---|
| Task not yet admitted | Conditionally establish the immutable input and recoverable execution state before Ack |
| Same delivery ID, matching immutable input | Verify durable acceptance; preserve captured rule settings, waiting/running/retry/terminal state, ownership, attempt history, and retry deadline |
| Same ID, conflicting immutable input | Report an explicit conflict; do not overwrite or authorize another execution |
| Write failed or commit outcome unknown | Withhold Ack until durable acceptance is verified; do not execute from volatile receipt alone |

Stable logical delivery identity is required. A document ID or per-process set
does not distinguish all tasks or deduplicate across replicas and republication.
Deduplication applies within the declared retention and replay horizon.
Database technology, schema, and conditional-write implementation remain open.

### Acknowledgement and Execution

- Ack means the durable task store has accepted responsibility, not that HTTP
  delivery succeeded. In the normal receive path, commit precedes Ack, then the
  receiver may wake an executor.
- Recovery eligibility begins at the durable commit. It must not depend on an
  Ack-success flag or an in-memory notification: a crash after Ack must leave
  work discoverable from the database.
- Workers atomically claim eligible tasks across replicas using the shared
  ownership contract. A duplicate broker receipt does not enqueue another HTTP
  execution; an expired claim alone does not prove prior remote work stopped.
- Durable state owns execution attempts, retry eligibility, and outcomes. Broker
  delivery counts no longer determine attempt exhaustion in this proposed model.
  Preserve the captured timeout, retry limits, backoff, and Retry-After policy.
- Timeout begins immediately before an admitted attempt enters the worker.
  Database waiting and retry delays do not consume its budget.

This downstream handoff does not establish new source-checkpoint eligibility.
[Complete durable scheduling](../bug-fix/2026-09-07-trigger-publish-checkpoint-ordering.md)
still governs upstream progress. [Execution controls](../feature/2026-09-07-trigger-rule-execution-controls.md)
own rule-wide admission limits; they must share the same execution ownership.

### Decisions Required Before Implementation

- Stable-ID encoding and envelope requirements; durable schema, indexes, and
  acceptance transitions, including reuse of upstream outbox records.
- Atomic claim, renewal, fencing, and recovery rules; ambiguous HTTP outcomes and
  cancellation of obsolete attempts without claiming remote rollback.
- Durable retry scanning and attempt accounting; rejection/conflict disposition,
  retention, storage capacity, and bounded ingestion/backpressure.
- Store abstraction and required durability/conditional-write capabilities;
  startup recovery and deployment-mode behavior.

## Alternatives

**Renew the broker acknowledgement window** can protect healthy processing but
must cover client prefetch, local waiting, execution, and shutdown. Progress
messages do not establish shared durable task outcomes or resolve uncertain HTTP
effects. Renewal may help the shorter persistence phase; it is not the proposed
execution-ownership mechanism.

**Check a local active-task set** suppresses duplicates within one process but
cannot arbitrate another replica. A database check followed by an unconditional
insert or execution also races; admission and claims require atomic operations.

**Increase AckWait** reduces premature redelivery for known workloads, but queue
residence and task duration can exceed it, while consumer-loss recovery slows.

## Acceptance Criteria

| Failure or concurrency case | Required result |
|---|---|
| Crash before durable acceptance | Broker can redeliver; no volatile-only execution |
| Commit succeeds, Ack fails or its result is unknown | Repeated receipt verifies the same task without resetting its state |
| Ack succeeds, process exits before notifying a worker | Database recovery discovers eligible work |
| Concurrent receipts or worker claims | One authoritative task and at most one valid execution claim |
| Duplicate waiting, running, retry-delayed, or completed task | No extra execution or attempt increment caused solely by receipt |
| Worker failure or uncertain HTTP result | Persist retry/recovery or ambiguity according to the defined ownership policy |
| Database outage or capacity exhaustion | Observable bounded backpressure; no successful handoff without persistence |

## Risks

Database writes, indexes, recovery scans, and retained payloads add capacity and
access-control costs. Once NATS is acknowledged, database loss or broken recovery
can lose work; an insert followed by an in-memory-only queue is insufficient.
Retaining identities too briefly permits old deliveries to create new work.

Atomic claims prevent simultaneous valid ownership, not exactly-once external
effects. A timed-out or disconnected worker may have already caused a remote
effect; receiver cooperation and the idempotency proposal remain necessary.
Until this draft is implemented, existing broker-managed retry and recovery
behavior remains in effect.
