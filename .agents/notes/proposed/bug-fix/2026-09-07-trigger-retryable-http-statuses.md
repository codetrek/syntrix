# Agent Note: Retry Rate-Limited Trigger Deliveries

Status: proposed

## Problem

[The HTTP worker](../../../../internal/trigger/delivery/worker/worker.go) classifies
all 400–499 responses as fatal. The consumer terminates fatal tasks immediately,
so HTTP 429 bypasses the task retry policy. This differs from the explicit 429
retry behavior in the [consumer design](../../../../docs/design/server/trigger/delivery/01.consumer.md).
The discrepancy is established by reading both paths.

## Proposal

Centralize the delivery outcome classification used by the worker and consumer:
2xx succeeds; HTTP 429, 5xx, transport errors, and attempt timeouts are retryable;
other 4xx responses are terminal unless a separately documented contract changes
them. State the existing handling of remaining non-success statuses explicitly.
Retain the response status and error chain through the worker boundary.

For 429, carry an optional parsed `Retry-After` delay to the consumer. Support
both delay-seconds and HTTP-date forms using a controllable clock. Compute a
bounded next-attempt delay from the response hint and rule backoff, respecting
the documented maximum delay and maximum attempts. Invalid or absent hints use
the rule backoff. Do not sleep while holding a worker slot: schedule delayed
redelivery through the queue.

Keep retry accounting in one owner so queue delivery metadata, task limits,
and terminal outcomes cannot disagree. Record status category, attempt number,
scheduled delay, and stable task identity without recording response bodies or
request payloads. Document the terminal outcome when the attempt budget expires.

## Alternatives

**Retry every 4xx response** is easy to classify but repeatedly calls endpoints
with invalid credentials, routes, or payloads and consumes the retry budget
without evidence that waiting helps.

**Ignore `Retry-After` and use rule backoff only** fixes the immediate lost-retry
bug, but can continue sending requests during an endpoint's requested pause.
It remains suitable only if the public contract explicitly excludes response
hints and operators accept that behavior.

## Acceptance Criteria

- A scripted endpoint returning 429 then 2xx causes bounded redelivery and one
  successful acknowledgement; ordinary terminal 4xx responses are not retried.
- 5xx, network errors, and timeouts retain their documented retry behavior.
- Valid, invalid, past-date, and excessive `Retry-After` values produce the exact
  documented bounded schedule under a controlled clock.
- Attempt exhaustion terminates with an identifiable outcome; shutdown during
  a delay leaves the task recoverable and does not retain an active worker.

## Risks

Retry hints can be very large and retries can amplify endpoint overload.
Clock skew affects HTTP-date parsing. Retried requests can repeat side effects,
so receivers need the stable identity from
[delivery idempotency](../architecture/2026-09-07-trigger-delivery-idempotency.md).
