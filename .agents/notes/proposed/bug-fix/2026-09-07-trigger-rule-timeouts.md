# Agent Note: Honor Trigger Rule Timeouts

Status: proposed

## Problem

[Task construction](../../../../internal/trigger/evaluator/service.go) assigns
`Timeout: types.Duration(types.DefaultTaskTimeout)` even when a loaded rule has
its own timeout. [The consumer](../../../../internal/trigger/delivery/consumer.go)
already creates a task deadline, while
[the worker](../../../../internal/trigger/delivery/worker/worker.go) independently
uses an HTTP client timeout. Static inspection shows that the requested rule
value is lost before delivery and that two timeout mechanisms can disagree.

## Proposal

Normalize and validate the rule timeout when rules load, then copy that effective
value into every delivery task. Preserve the documented default only for an
omitted value; reject negative or out-of-range durations. Define the timeout as
the execution budget for one attempt, starting after queue and admission waits.
It includes secret resolution, token minting, and the HTTP operation.

Use the task context as the authoritative total execution deadline. Transport
connection and response-header limits may remain separately documented resource
limits, but the HTTP client's total timeout must not silently truncate a valid
longer rule timeout. All participating operations must observe cancellation.

Retried tasks retain the captured rule timeout; a later rule edit affects newly
created tasks. Coordinate timeout bounds with broker acknowledgement handling so
a valid long attempt is not concurrently redelivered because its acknowledgement
window expired. Emit a timeout category and attempt identity for diagnostics.

## Alternatives

**A single service timeout** simplifies operations but cannot meet the existing
per-rule setting's intent. It would require explicitly removing that setting.

**The minimum of rule and service timeouts** provides a global cap, but is
misleading if silently applied. A declared maximum validated at rule load gives
operators the same cap with a visible configuration error.

## Acceptance Criteria

- Rules with different supported timeouts produce tasks with those exact values;
  omission uses the documented default and invalid durations fail activation.
- A controllably stalled endpoint is cancelled at the selected attempt deadline,
  while a request longer than the old HTTP default can complete when allowed.
- Cancellation during secret resolution or HTTP work ends the attempt and frees
  its execution slot; retries remain bounded by the task's attempt policy.
- Queue waiting does not consume the execution budget, and broker redelivery
  does not overlap a still-valid attempt solely because of timeout mismatch.

## Risks

Longer deadlines retain sockets and worker capacity. Cancellation does not undo
remote side effects, so receivers still need
[delivery idempotency](../architecture/2026-09-07-trigger-delivery-idempotency.md).
Timing checks need controlled clocks or generous bounded tolerances.
