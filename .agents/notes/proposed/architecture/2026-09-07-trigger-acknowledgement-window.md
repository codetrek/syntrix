# Agent Note: Coordinate Trigger Acknowledgement Windows

Status: proposed

## Problem

The [rule timeout repair](../../implemented/bug-fix/2026-09-07-trigger-rule-timeouts.md)
starts each execution budget after local queue waiting. Broker acknowledgement
timing already runs while a delivered message waits in local queues and while
the attempt executes. A long wait or valid long attempt can therefore outlive the
acknowledgement window and permit concurrent redelivery. Honoring rule timeouts
alone does not prevent overlapping attempts.

## Proposal

Define acknowledgement coordination across queue residence and execution so a
valid active attempt is not concurrently redelivered solely because its broker
acknowledgement window expires. Preserve the task's captured timeout and the
consumer's ownership of attempt accounting. The execution budget still starts
after queue waiting; acknowledgement ownership must cover that earlier wait too.

Select and validate the coordination mechanism before implementation. No lease,
heartbeat, acknowledgement extension, or timeout cap is selected by this note.
The decision must define queue interface support, ownership from receipt through
terminal acknowledgement or retry scheduling, failure propagation, cancellation,
and shutdown. It must also define behavior when coordination fails or ownership
is lost; expiry alone does not prove an HTTP side effect stopped.

Keep this proposal's ownership narrow. The
[delivery idempotency proposal](2026-09-07-trigger-delivery-idempotency.md) owns
stable identity, durable task claims, fencing, and ambiguous external effects.
The [execution-controls proposal](../feature/2026-09-07-trigger-rule-execution-controls.md)
owns cross-replica rule admission and rate/concurrency budgets. Acknowledgement
coordination must fit those ownership boundaries without introducing a competing
delivery ledger or admission mechanism.

Deferral preserves existing broker behavior: queue waiting and execution can
outlast its acknowledgement window. Closing the gap later requires evaluating
broker capabilities and queue interfaces, and coordinating the consumer's queue,
attempt, cancellation, and acknowledgement lifecycles. The current timeout
boundary must remain explicit so this work cannot be mistaken for increasing
the rule's execution budget. Diagnostics must distinguish queue residence,
execution deadline, and acknowledgement ownership using available correlation
fields without logging sensitive task payloads or claiming stable identity before
that capability exists.

## Alternatives

No coordination mechanism has been selected. Leaving acknowledgement behavior
unchanged allowed the rule-timeout repair to preserve the existing queue protocol,
but accepts overlapping redelivery during long waits or attempts. This proposal
retains the missing guarantee and the coordination cost required to deliver it.

## Acceptance Criteria

- Queue residence and execution are both covered by the defined acknowledgement
  ownership; queue waiting does not consume the per-attempt execution budget.
- A queued task or still-valid attempt is not concurrently redelivered solely
  because its acknowledgement window expires.
- Loss of coordination, cancellation, retry scheduling, acknowledgement failure,
  and shutdown produce the documented ownership and recovery outcomes without
  masking failures or creating an unbounded wait.
- Retries preserve the captured timeout and existing attempt accounting. Rule
  updates affect new tasks without rewriting the timeout on already queued work.
- Diagnostics distinguish timeout from acknowledgement ownership failure, and
  the guarantee states its client-attempt boundary without promising that HTTP
  cancellation undoes remote work.

## Risks

Covering queue residence can retain broker ownership for a long time and delay
recovery from failed consumers. Coordination failure can require cancellation or
fencing that the current queue interface does not express. Deployment-wide
admission and durable claims must share compatible ownership semantics.

Local synchronous signing cannot be context-preempted, and remote effects may
continue after client cancellation. The design must preserve these limits while
defining when replacement attempts may begin. Queue-backend recovery guarantees
remain explicit; this proposal does not confer restart durability on the
in-memory queue.
