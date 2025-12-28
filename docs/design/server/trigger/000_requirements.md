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
- Enforce tenant isolation via subject prefixing on publish/consume and tenant-scoped checkpoints so resume tokens do not leak across tenants.

## Non-Goals

- Changing business semantics of trigger evaluation, retry math, or CEL language.
- Reworking storage.Event or storage.Document shapes.
- Introducing new external dependencies.

## Constraints

- Follow existing doc structure and naming; code and docs in English.
- Tests must continue using testify; no integration tests that bypass public interfaces.
- Preserve compatibility with current NATS subject format and worker HTTP contract unless explicitly revised later.
- NATS subject safety: docKey portion must be subject-safe (base64url without padding); publisher/consumer share the same encoding/decoding path.
- Secrets/tokens are tenant-scoped; cross-tenant reuse is forbidden.
- Shared `TRIGGERS` stream is assumed; monitor per-tenant traffic/retention and be ready to pivot to per-tenant streams if quotas/noisy-neighbor risks emerge.

## Observability baseline

- Emit per-tenant metrics for publish/consume/worker success, retry counts, and latency; tracing is optional but recommended for incident drills.
- Enforce subject-length guardrails: if encoded subject would exceed NATS limits, hash the docKey portion for the subject while keeping the original (decoded) docKey in the payload for worker use.
- Metrics shape: at minimum expose counters for publish/consume/worker success and retries, and histograms for end-to-end latency, all tagged with tenant and collection.
- Subject-length guard details: use NATS subject max of 1024 bytes; if `triggers.<tenant>.<collection>.<encodedDocKey>` exceeds this, replace `encodedDocKey` with `sha256(<encodedDocKey>)` hex truncated to 32 chars; always carry the original decoded docKey in the payload.
- Hash fallback flagging: when subject hashing occurs, set a boolean marker in payload/metrics (e.g., `subjectHashed=true`) to aid debugging and collision investigations.

## Known Issues (Dec 2025 review)

> **Moved to Task 010**: The known issues list has been moved to `tasks/010.2025-12-28-trigger-engine-refactor.md` to serve as a checklist for the refactor.

## Open Questions

- Multi-tenant isolation: decided to keep a single `TRIGGERS` stream with subject prefixing (`triggers.<tenant>.>`) for routing; revisit only if quotas/retention require per-tenant streams.
- Checkpoints: must be tenant-scoped keys (e.g., `sys/checkpoints/trigger_evaluator/<tenant>`) to avoid cross-tenant resume token sharing.
- Observability: none beyond the above baseline; tracing hooks are nice-to-have.
