# Agent Note: Integrate Puller Health Reporting

Status: proposed

## Problem

The [running Puller](../../../../internal/puller/core/puller.go) now owns a
checker, records backend connection/failure and committed-event transitions,
updates subscription counts, and exposes `HealthReport`. This supports lifecycle
failure propagation, but [service assembly](../../../../internal/services/manager_init.go)
does not register the configured dedicated health endpoint. The existing helper
still hardcodes `/health` while [configuration](../../../../internal/puller/config/puller.go)
contains a path and port.

A complete operational readiness contract still needs endpoint ownership,
startup/reconnection states, documented liveness separation, and buffer units.
Source bootstrap and continuity failure are implemented separately and must
remain intact while health integration is completed.

## Proposal

Complete the owned Puller health snapshot with backend and buffer lifecycle:
starting, connected, reconnecting, history unavailable, failed, and stopped.
Report readiness only when required backends can deliver a continuous stream;
report process liveness separately so transient dependency failure does not
automatically trigger restart loops. A lack of document changes is not itself
a failure when the upstream stream remains healthy.

Have the service manager register the report and manage its endpoint in both
standalone and distributed modes. Honor the configured path and port, propagate
bind/start failures, and close listeners with the service shutdown deadline.
Define aggregation for partial backend failure, event-count versus byte units,
consumer counts, and startup readiness. Use the existing HTTP facilities where
appropriate; a new endpoint implementation or dependency is not predetermined.

## Alternatives

**Expose the existing checker unchanged.** This delivers an endpoint quickly,
but default-healthy registrations and event-only recovery cannot represent a
failed or idle upstream connection accurately.

**Use only generic process health.** This detects a dead process but cannot tell
operators whether one configured event source has stopped delivering.

## Acceptance Criteria

- Production standalone and distributed startup expose the configured health
  endpoint, and a bind failure reaches startup error handling.
- Deterministic backend connect, retry, fatal failure, and recovery transitions
  produce documented per-backend and aggregate readiness states.
- An idle connected backend stays healthy; history loss remains visible until
  recovery, and one failed backend cannot be masked by events from another.
- Consumer and buffer values use documented units, and cancellation removes the
  listener and health workers within a bounded timeout.

## Risks

Overly strict readiness can remove all replicas during dependency outages;
overly permissive readiness conceals broken ingestion. Health responses and
logs must omit resume tokens, credentials, and document payloads.

## Dependencies

[History-gap recovery](../../implemented/architecture/2026-09-07-puller-history-gap-recovery.md)
owns continuity state;
[application observability](../architecture/2026-09-07-application-observability.md)
owns generic telemetry and deployment probe conventions.
