# Server-side Triggers & Webhooks Discussion

**Date:** December 15, 2025
**Topic:** Database Triggers, Webhooks, and External Integrations

## 1) Architecture & Module Boundary

- Source: database change streams (CRUD events) feed trigger evaluation.
- Module: dedicated trigger-delivery service (separate from API/CSP/Gateway) handles outbound calls, retries, and DLQ to isolate latency/failures.
- Execution model: asynchronous via durable queue + workers; synchronous (inline) mode deferred for future consideration.
- Partitioning (Why: fairness and isolation; How):
  - Partition queue by (tenant, collection, docKey hash); cap outstanding work per partition to stop a single hot document from starving others.
- Storage (Why: durable handoff; How):
  - Use NATS JetStream with file storage; rely on JetStream durability instead of intermediate persistence before enqueue.
- Resource limits (Why: prevent noisy neighbors and runaway payloads; How):
  - Max outbound payload 256 KB (body); headers capped at 16 KB; async worker request timeout 5 s; max retries per item 10 with jitter; per-tenant concurrency cap 64 workers (configurable).

Data flow (logical):

```text
Change Stream -> Trigger Evaluator (condition match) -> NATS JetStream (partitioned subject) -> Trigger Delivery Workers -> Webhook endpoints / DLQ
```

## 2) Trigger Definition & Matching

- Conditions: reuse the CEL subset expression language (from 002) for trigger conditions.
- Events: create/update/delete; payload includes before/after snapshots, tenant, collection, docKey, lsn/seq.
- Idempotency: key = (tenant, triggerId, lsn/seq) to dedupe downstream.
- Config shape (Why: consistent rollout; How):
  - `triggerId`, `version`, `tenant`, `collection`, `events[]`, `condition`, `url`, `headers`, `secretsRef`, `concurrency`, `rateLimit`, `retryPolicy`, `filters` (optional path list).
- Evaluator (Why: safety and predictability; How):
  - Compile conditions once per config version; restrict CEL functions to deterministic ops; reject unbounded glob-style filters.
- Ordering modes (Why: make trade-offs explicit; How):
  - Standard: partitioned by (tenant, collection, docKey hash) allows parallel delivery; guarantees order per document. Strict global ordering is not supported in this phase.

## 3) Delivery & Reliability

- Semantics: at-least-once; workers dequeue, sign, deliver, and ack.
- Retries: exponential backoff with max attempts; DLQ on exhaustion or fatal responses (e.g., 4xx configurable).
- Ordering: guaranteed per document (via partition key).
- Backpressure: per-tenant/per-trigger rate limits and concurrency caps to avoid noisy neighbors.
- Retry policy (Why: avoid thundering herds; How):
  - Jittered exponential backoff; classify responses: retry on 429/5xx/timeouts, drop or DLQ on explicit 4xx (configurable list).
- Poison handling (Why: avoid infinite retries; How):
  - Detect repeated fast-fail cycles; short-circuit to DLQ with a succinct error snapshot (status, headers hash, truncated body, attempt count).
- DLQ replay (Why: recovery path; How):
  - Replay requires operator action; replay preserves original idempotency key and appends a replay reason; support partial replay by tenant/trigger/time range.
  - Replay governance (Why: auditability; How): require operator identity + reason logged, dry-run preview of count/size before execution, and metrics on replay success/failure.

## 4) Webhook Security

- Transport: HTTPS required.
- Integrity: HMAC signature over body + timestamp (per-tenant secret); support secret rotation with overlap window.
- Size/timeout: cap request/response size; enforce timeouts; configurable headers.
- Secret handling (Why: least privilege; How):
  - Store secrets in a secret manager; workers fetch via short-lived tokens; keep dual-secret window during rotation; enforce clock-skew tolerance on timestamped signatures.
- Signature tolerance (Why: avoid false rejects; How): default timestamp skew tolerance ±300s; reject outside window with explicit error code for observability.
- Note: Advanced security features like mTLS and fixed egress IPs are deferred.

Example webhook payload + signature:

```json
{
  "triggerId": "trg_123",
  "tenant": "acme",
  "event": "update",
  "collection": "orders",
  "id": "abc123",
  "lsn": "1697041234:42",
  "seq": 99102,
  "before": { "id": "abc123", "collection": "orders", "status": "pending", "version": 1 },
  "after":  { "id": "abc123", "collection": "orders", "status": "shipped", "version": 2 },
  "ts": 1697041234
}
```

Header: `X-Syntrix-Signature: t=1697041234,v1=hex(hmac_sha256(secret, t + "." + body))`

## 5) Admin & Operations

- Management API/CLI: create/update triggers (url, events, condition, headers, auth), rotate secrets, view status and metrics, inspect/replay DLQ, test delivery (dry-run).
- Observability: per-trigger metrics (success/fail/latency, retry counts, DLQ size), structured logs with requestId/triggerId/tenant.
- Rollout: trigger configs are versioned; allow staged rollout/disable; health checks on endpoints before enabling.
- Audit (Why: traceability; How):
  - Record who changed what (user, time, diff) for trigger configs; emit change events to an audit log; require signatures for sensitive updates (e.g., URL/secret changes).
- Dry-run (Why: safe rollout; How):
  - Dry-run mode evaluates conditions and records would-be deliveries without sending; compare hit rates before enabling real delivery.
- Staged rollout (Why: reduce blast radius; How):
  - Support percentage- or tenant-scoped enablement; auto-roll back on error rate or latency SLO breach.

## 6) Future Extensibility

- Reserved path for sandboxed functions (JS/Go) running out-of-process with CPU/mem/time limits, reusing the same event model and delivery semantics.
- Transform hooks (Why: adapt payloads without webhook churn; How):
  - Allow lightweight, bounded transformations (e.g., field masks, templating) executed in the sandboxed function path with strict limits; keep default path passthrough for latency-sensitive triggers.

## 7) Trigger-Internal Read/Write Interface & Auth (Draft)

**Why**: Triggers may need to read or write documents as part of their processing pipeline. We want a minimal, safe, and auditable path distinct from public APIs while still enforcing CEL rules by default.

### **Endpoints (internal-only)**

- `POST /api/v1/trigger/get`: batch document reads by concrete paths.
- `POST /api/v1/trigger/query`: single query (same shape as public query API).
- `POST /api/v1/trigger/write`: one or more write ops in a single request.

### **Parameters & Response**

- Common: Auth via TET (tenant inferred), JSON body; errors use standard codes listed below.
- `/trigger/get`
  - Request: `{ "paths": ["/rooms/room-123/messages/msg-9", ...] }` (paths required, non-empty)
  - Response: `{ "documents": [{"id": "...", "collection": "...", "version": 7, "updatedAt": 123, "<user_fields>": "..."}, ...] }`

## 8) Trigger Authentication Implementation

### **Mechanism**: System Tokens (JWT)

- The Trigger Worker authenticates using a "System Token" signed by the same private key as user tokens.
- **Claims**:
  - `sub`: `system:trigger-worker`
  - `roles`: `["system", "service:trigger-worker"]`
- **Validation**:
  - Standard JWT signature validation.
  - Middleware checks for `system` role for `/api/v1/trigger/*` endpoints.
  - Regular user tokens (without `system` role) are rejected with 403 Forbidden.
    - Order matches input `paths`.
    - Missing doc returns a placeholder `{ "path": "<input>", "missing": true }` (HTTP 200 overall); if caller prefers hard fail, use `/trigger/query` with filters.
  - `/trigger/query`
    - Request: `{ "query": { "collection": "rooms/room-123/messages", "filters": [...], "orderBy": [...], "limit": 20, "startAfter": "cursor" } }`
    - Response: `{ "documents": [{"id": "...", "collection": "...", "version": 7, "updatedAt": 123, "<user_fields>": "..."}, ...], "nextCursor": "..." }` (identical to public query API)
  - `/trigger/write`
    - Request: `{ "ops": [{ "type": "create|set|update|patch|delete", "path": "...", "data": {...}, "precondition": [{"field": "version", "op": "==", "value": 7}] } , ...], "idempotencyKey": "..." }` (one or many ops)
    - Response (transactional, all-or-nothing):
      - Success: `{ "results": [{ "status": "ok", "version": 8, "updatedAt": 1234567891 }, ...], "idempotencyKey": "..." }`
      - Failure (any op fails → none applied, including precondition mismatch): `{ "error": { "code": "...", "message": "...", "details": [{"opIndex": 0, "code": "PRECONDITION_FAILED", "message": "version mismatch"}, ...] }, "idempotencyKey": "..." }`

### **Request shape (examples)**

- Get: `{ "paths": ["/rooms/room-123/messages/msg-9"] }`
- Query: `{ "query": { "collection": "rooms/room-123/messages", "filters": [...], "orderBy": [...], "limit": 20, "startAfter": "cursor" } }` (structure identical to `POST /api/v1/query`)
- Write: `{ "ops": [ { "type": "create|set|update|patch|delete", "path": "/rooms/room-123/messages/msg-9", "data": {...}, "precondition": [{"field": "version", "op": "==", "value": 7}] }, { "type": "delete", "path": "/rooms/room-123/messages/msg-10" } ], "idempotencyKey": "..." }`

Notes:

- `tenant` is derived from the TET; it is not a request field.
- Consistency level follows service defaults (strong for single-doc, eventual/strong as implemented by query engine); no caller flag.

### **Response shape alignment**

- `/trigger/get`: array of documents mirroring `GET /api/v1/{path}` shape per item.
- `/trigger/query`: same as `POST /api/v1/query` (`documents`, `nextCursor`).
- `/trigger/write`: transactional; either all ops succeed with per-op results, or none are applied and an error with per-op details is returned.
- Document payloads use the same flattened shape as public API (i.e., `flattenDocument`: user fields plus `id`, `collection`, `version`, `updatedAt`).

### **Token issuance (TET)**

- Issued by control plane for a specific trigger execution window (short TTL, e.g., minutes).
- Contains claims listed below; signed with service key (RS256/EdDSA). Issuance resolves all placeholders into concrete prefixes for `allow_paths`.
- Delivery: passed to the trigger executor (internal worker or trusted runtime) and attached as Authorization bearer in each call.

### **Auth model: Trigger Execution Token (TET)**

- Short-TTL JWT/EdDSA issued by control plane; every trigger read/write call must present this temporary token.
- Claims: `sub` (triggerId), `tenant`, `scopes` (`trigger.read`, `trigger.write`), `allow_paths` (concrete prefixes), optional `max_ops`, `max_payload_kb`.
- Placeholder values must be resolved at issuance time: `allow_paths` contains concrete prefixes (e.g., `/rooms/room-123/messages/`), no templates remain.
- Validation pipeline: verify signature/expiry → tenant match → scope check → path/prefix enforcement → rate limiting by `(tenant, triggerId)`.
  - Document reads/writes: every `path` must start with one of `allow_paths`.
  - Query reads: `collection` must start with one of `allow_paths`; cross-collection/join not allowed via this endpoint.
  - Limit checks: enforce `max_ops`, `max_payload_kb` (if present) and return `429 RESOURCE_EXHAUSTED` when exceeded.

### **Authorization (rules)**

- CEL rules are enforced by default for trigger read/write; no bypass unless explicitly configured in control plane (not default). If bypass is ever enabled, it must be coupled with strict `allow_paths` narrowing and audited.

### **Idempotency & preconditions**

- `Idempotency-Key` is **required** for write/batch. Dedup key space: `(tenant, triggerId, key)`; repeat returns `409 IDEMPOTENCY_REPLAY` or the first result.
- CAS/preconditions (optional): `ifMatchVersion`, `ifModifiedSince` guard against blind overwrites; failures return `412 PRECONDITION_FAILED`.

### **Additional semantics & defaults**

- Preconditions/filters: same operator set and semantics as public query filters; any field is allowed (including user fields), e.g., `version == 7`.
- Precondition operators: `==, !=, >, >=, <, <=, in, not-in, array-contains` (align with public query); multiple preconditions in an op are ANDed.
- Patch semantics: identical to public PATCH (partial update; preserves unspecified fields; supports nested field paths as in public API; no special restrictions beyond existing reserved fields).
- Field restrictions: `set/patch/update` cannot write reserved fields (`_id`, `_path`, `_parent`, `collection`, `updatedAt`, `version`, system-only keys); attempts return 400 INVALID_ARGUMENT.
- Op order & duplicates: in a single `/trigger/write` transaction, multiple ops on the same path are allowed and executed in request order; later ops see effects of earlier ones (no dedup to reduce overhead).
- Limits: `max_ops`, `max_payload_kb`, default op count/size/limit/timeout are provided via config (not in token unless overridden).
- Error codes: HTTP status codes are reused; application codes align to the following mapping (stable set):
  - 400 `INVALID_ARGUMENT`
  - 401 `UNAUTHENTICATED`
  - 403 `PERMISSION_DENIED` (includes `RULE_DENY`, `PATH_NOT_ALLOWED`, `INVALID_SCOPE`)
  - 404 `NOT_FOUND`
  - 409 `IDEMPOTENCY_REPLAY`
  - 412 `PRECONDITION_FAILED`
  - 429 `RESOURCE_EXHAUSTED` (limits, rate, size, ops)
  - 500 `INTERNAL`
- Idempotency behavior: replaying the same `Idempotency-Key` returns only the stored outcome (success/failed) without per-op details to minimize storage overhead.
- Idempotency replay status: a replay returns the same HTTP status and top-level outcome as the first execution (success or failure), but omits per-op detail. If the record expired (TTL), the request is treated as new.
- Query constraints: `/trigger/query` does not allow cross-collection/collection-group queries; `limit` default and max via config; missing required index returns `MISSING_INDEX` (HTTP 400).
- Delete preconditions: `delete` supports preconditions; deleting a non-existent doc returns `404 NOT_FOUND`.
- Idempotency cache: stored in Redis (or equivalent); TTL configured via config (default 8h); stores only outcome (success/failure), not per-op details.
- Transaction timeout: configurable, default 5s; on timeout the transaction is rolled back and returns timeout error (HTTP 500/408 per implementation).
- Missing index: `MISSING_INDEX` (HTTP 400) placeholder until index creation flow exists.
- Array/nested writes: follows public PATCH/UPDATE semantics—nested fields supported; arrays are replaced as whole values (no push/pull/inc operators supported); use full value replacement for array changes. No server-side partial array mutations.
- Concurrency conflicts: on storage conflict (e.g., CAS/version mismatch without matching precondition), apply a short delay and retry once; if still failing, return `412 PRECONDITION_FAILED` (or storage-specific conflict code).
- Concurrency retry backoff: default 25ms (configurable) before the single retry.
- Transaction timeout code: use 408 `REQUEST_TIMEOUT` (preferred) on timeout/rollback; 500 reserved for unexpected server errors.
- Missing index error shape: `{ "code": "MISSING_INDEX", "message": "Query requires index", "suggestedIndex": { "collection": "...", "fields": [{"path": "data.status", "direction": "asc"}, ...] } }`.

### **Error semantics (draft)**

- `403 PERMISSION_DENIED`: auth failed (`INVALID_SCOPE`, `PATH_NOT_ALLOWED`, token expired).
- `403 RULE_DENY`: CEL rule denied access.
- `409 IDEMPOTENCY_REPLAY`: duplicate `Idempotency-Key`.
- `412 PRECONDITION_FAILED`: CAS/precondition not met.
- `429 RESOURCE_EXHAUSTED`: rate/size/ops limits exceeded.

### **Observability & audit**

- Metrics tagged by `tenant`, `triggerId`, `scope` (read/write), `result`.
- Audit log fields: `triggerId`, `tenant`, `paths`, `scopes`, `idempotencyKey`, `result`, `errorCode`.

## 8) Implementation Details (v1)

### 8.1 Trigger Configuration

The `Trigger` configuration struct includes the following fields:

- `triggerId`: Unique identifier.
- `version`: Config version.
- `tenant`: Tenant identifier.
- `collection`: Collection name or glob pattern (e.g., `users/*`).
- `events`: List of events (`create`, `update`, `delete`).
- `condition`: CEL expression string.
- `url`: Webhook destination URL.
- `headers`: Custom headers map.
- `secretsRef`: Reference to a secret for signing.
- `concurrency`: Max concurrent workers.
- `rateLimit`: Rate limit per second.
- `includeBefore`: Boolean to include the document state before the change.
- `retryPolicy`: Struct with `maxAttempts`, `initialBackoff`, `maxBackoff`.
- `filters`: List of path filters.
- `timeout`: Request timeout duration (e.g., "5s").

### 8.2 Webhook Payload

The payload sent to the webhook is a JSON representation of the `DeliveryTask` struct.
**Note:** Currently, this includes internal delivery details.

```json
{
  "triggerId": "...",
  "tenant": "...",
  "event": "...",
  "collection": "...",
  "docKey": "...",
  "lsn": "...",
  "seq": 123,
  "before": { ... },
  "after": { ... },
  "ts": 1234567890,
  "url": "...",
  "headers": { ... },
  "secretsRef": "...",
  "retryPolicy": { ... },
  "timeout": "..."
}
```

### 8.3 Headers

The following headers are sent with the webhook request:

- `Content-Type`: `application/json`
- `User-Agent`: `Syntrix-Trigger-Service/1.0`
- `X-Syntrix-Signature`: `t={timestamp},v1={hex(hmac_sha256(secret, t + "." + body))}`
- `Authorization`: `Bearer <system-token>` (Internal system token, currently sent to all destinations)
- Custom headers defined in the trigger configuration.

### 8.4 CEL Evaluation Context

The CEL environment provides an `event` variable with the following structure:

- `event.type`: String (`create`, `update`, `delete`)
- `event.timestamp`: Integer timestamp
- `event.document`: Map representing the current document state (for `create` and `update`).
- `event.before`: Map representing the previous document state (if available).

Collection matching uses `path.Match`, supporting shell file name patterns.

### 8.5 Internal API & Security

The internal trigger endpoints (`/api/v1/trigger/...`) are protected by a system role check.

- **Middleware**: `triggerProtected` ensures the caller has `role: system`.
- **Worker Authentication**: The delivery worker generates a system token (signed with internal key) to authenticate against these endpoints if needed, or to authenticate itself to the webhook destination (current behavior).

### 8.6 Reliability & Checkpointing

The Trigger Service maintains a checkpoint to ensure at-least-once processing of the change stream.

- **Storage**: Checkpoints are stored in the `sys` collection under `sys/checkpoints/trigger_evaluator`.
- **Resume**: On startup, the service loads the last processed resume token.
- **Update**: The checkpoint is updated after processing each event (synchronously in v1).

### 8.7 System Token in Payload

The system token is now included in the `DeliveryTask` payload as `systemToken`, instead of being sent as an `Authorization` header. This allows the webhook receiver to parse the token from the JSON body and use it for callbacks.
