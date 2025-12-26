# Trigger Refactor Requirements

## Why

- Align trigger subsystem with storage-style factory/provider pattern to hide backend/NATS/HTTP details from callers.
- Reduce coupling so trigger engine can swap transport or worker implementations without touching service consumers.
- Improve testability with clear interfaces and injectable fakes.

## Goals

- Provide a single trigger entrypoint (engine) exposing only minimal lifecycle and trigger-loading APIs.
- Encapsulate NATS, HTTP worker, and storage watch/checkpoint details behind internal adapters.
- Keep configuration centralized and declarative (mirrors storage config style).
- Maintain current behaviors (event filtering, CEL evaluation, retry/backoff semantics) while clarifying boundaries.

## Non-Goals

- Changing business semantics of trigger evaluation, retry math, or CEL language.
- Reworking storage.Event or storage.Document shapes.
- Introducing new external dependencies.

## Constraints

- Follow existing doc structure and naming; code and docs in English.
- Tests must continue using testify; no integration tests that bypass public interfaces.
- Preserve compatibility with current NATS subject format and worker HTTP contract unless explicitly revised later.

## Open Questions

- Do we need multi-tenant isolation at the stream/consumer level beyond subject prefixing?
- Should checkpoints migrate to a dedicated collection/key or remain at `sys/checkpoints/trigger_evaluator`?
- Any requirement to expose metrics/observability hooks in the new API?
