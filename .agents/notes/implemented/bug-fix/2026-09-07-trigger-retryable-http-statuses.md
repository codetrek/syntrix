# Agent Note: Retry Rate-Limited Trigger Deliveries

Status: implemented

## Problem

The HTTP worker classified every 400–499 response as fatal, so the consumer
terminated HTTP 429 tasks before applying their retry policy. This contradicted
the [consumer design](../../../../docs/design/server/trigger/delivery/01.consumer.md),
which requires rate-limited deliveries to retry with backoff.

## Decision

[The worker](../../../../internal/trigger/delivery/worker/worker.go) treats 429 as
a retryable failure. One status predicate determines both the failure metric's
fatal flag and whether the returned error is wrapped in `FatalError`: only
400–499 responses other than 429 are fatal. A 429 response returns the existing
ordinary error containing the response status. HTTP 2xx succeeds; all other
non-2xx responses retain their existing retryable classification. Transport
errors and attempt timeouts retain their error chains and retry behavior.

[The consumer](../../../../internal/trigger/delivery/consumer.go) remains the
owner of attempt accounting and scheduling. Fatal failures terminate immediately.
Retryable failures use queue delivery metadata and the task's total attempt
limit, including the initial delivery. Reaching the limit logs exhaustion and
terminates the message. Otherwise the consumer computes the existing exponential
backoff and calls `NakWithDelay`, releasing the worker without sleeping.

Response hints do not affect scheduling. The separate
[Retry-After proposal](../../proposed/feature/2026-09-07-trigger-retry-after.md)
owns header parsing, hint propagation, and their interaction with delay limits.
The worker continues to classify the response, and the consumer continues to
schedule retries; future hint support can extend that boundary without moving
retry ownership or changing the task's attempt budget.

## Alternatives

**Retry every 4xx response** would repeatedly call endpoints with invalid
credentials, routes, or payloads and consume attempts without evidence that
waiting helps.

**Deliver Retry-After support together with status classification** would also
respect endpoint-requested pauses, but requires choosing hint parsing and delay
rules beyond the existing 429 retry contract. The independent proposal preserves
those decisions and acceptance criteria. Rule-only backoff is the delivered
behavior and is explicit in the [public guide](../../../../docs/reference/trigger_rules.md).

## Consequences

Rate-limited tasks now enter the same bounded-attempt retry path as other
retryable failures, and failure metrics classify 429 consistently with delivery.
Ordinary terminal 4xx responses still stop immediately. The change preserves
retry arithmetic, queue operations, redirect handling, and task format.

An endpoint's `Retry-After` pause is not honored, so a retry may arrive before
that pause ends. Adding support requires a worker-to-consumer hint representation
and bounded delay decisions; neither is supplied by this classification repair.
Retries can repeat external side effects. Stable delivery identity and durable
redrive remain owned by the proposed
[delivery idempotency design](../../proposed/architecture/2026-09-07-trigger-delivery-idempotency.md).
Queue-delayed retries retain the selected backend's existing recovery limits.
