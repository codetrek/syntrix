# Identity & Admin Control Plane

**Date:** December 18, 2025
**Topic:** Identity, token validation, and rules (self-hosted, Firestore-like)

## 1. Scope
- Build an internal auth service that issues JWTs from username/password sign-up/sign-in.
- Enforce Firestore-style CEL rules (deny-by-default) for all data access.
- Single-tenant, self-hosted focus; external IdPs out of scope for this iteration.
- Rule publish/rollback lives in the console doc: see [discussion/008_console.md](discussion/008_console.md).

## 2. Goals & Non-goals
- Goals:
	- Minimal, self-contained auth: sign-up, sign-in, sign-out (token revoke), password change.
	- JWT-based request authentication in `syntrix --api` and `--realtime`.
	- Rule evaluation identical to Firestore-style CEL rules (read/write conditions on resource data and request auth claims).
- Non-goals (now): multi-factor auth, social login, SSO, SCIM, org/tenant hierarchy, password reset via email/SMS. Admin UI is documented separately in [discussion/008_console.md](discussion/008_console.md).

## 3. Identity Service (How)
- **Data model**: logical collection `auth_users/{user_id}` (or SQL table `auth_users`) with: `id` (UUID), `username` (unique, stored lowercased for comparison), `password_hash`, `password_algo`, `created_at`, `updated_at`, `disabled` (bool), `roles` (string array, optional, max 10 items, each ≤32 chars), `profile` (opaque JSON).
- **Password hashing**: prefer `argon2id` (or `bcrypt` fallback) with per-user salt; store parameters alongside hash for future migration.
- **Sign-up**: username + password; validate strength; reject duplicates; persist auth record; return JWT (access) on success.
- **Sign-in**: username + password; verify hash; issue access token (≈15m) and refresh token (≈7d) if refresh is enabled.
- **Sign-out / revoke**: maintain server-side revocation set keyed by refresh `jti` (optionally access `jti`) with TTL; store in in-memory + persistent KV for cluster scope.
- **Password change**: require old password; rotate tokens.
- **Account disable**: `disabled=true` blocks login and is enforced during token validation (claim check + revocation cache lookup).
- **Audit**: append-only log for auth events (sign-up, sign-in, revoke, password change) with IP/user agent when available.

## 4. Token Strategy (How & Why)
- **Format**: JWT (signed, not encrypted). Algorithm: `RS256` (preferred) or `EdDSA`; avoid `HS256` to simplify key distribution across nodes.
- **Claims (minimal)**: `sub` (user id), `username`, `roles` (optional), `iat`, `exp`, `jti`, `disabled` flag.
- **Key management**: internal keyset (`kid` versioned); rotate regularly; publish JWKS endpoint for internal services.
- **Performance**: cache JWKS (5–10m) with background refresh; revocation cache in memory with upstream backing store; short access TTL to limit blast radius.

### 4.1 Default parameters (proposed)
- Access token TTL: 15 minutes (configurable, default 15m); Refresh token TTL: 7 days (configurable, optional feature flag).
- Hashing: `argon2id` with `t=3`, `m=64 MiB`, `p=1`, `keyLen=32`, per-user salt; fallback `bcrypt` cost=12 when argon2 unavailable. If neither is available, reject sign-up/login instead of silently downgrading.
- Password policy: min length 12, require 3 of 4 classes (upper/lower/digit/symbol); block common/known-breached passwords.
- Username policy: 3–64 chars, `[a-z0-9._-]`, unique, case-insensitive compare.
- Auth throttling: per-username and per-IP rate limit (e.g., 5 attempts / 15m, exponential backoff after 3 failures).
- Lockout: temporary lock 5 minutes after 10 consecutive failures; reset counter on successful login or after cooldown.
- Lock/attempt counters stored in shared KV (Redis/etcd) to survive restarts; on KV outage fall back to in-memory with stricter caps.

## 5. Token Validation Pipeline
1) Extract bearer token from `Authorization: Bearer <jwt>` or WebSocket initial auth message.
2) Decode header, pick `kid`; fetch signing key from JWKS cache (fallback to JWKS endpoint).
3) Verify signature (RS256/EdDSA), verify `exp`, `nbf`, `iat` skew.
4) Parse claims: `sub`, `username`, `roles`, `jti`, `disabled`.
5) Revocation check: in-memory LRU → shared store (Redis/etcd) keyed by `jti` and user state hash.
6) Disabled check: `disabled` claim must be false; optionally cross-check user cache keyed by `sub` version.
7) Build `request.auth` context and continue to authorization/rules.

## 6. Caching & Freshness
- JWKS cache TTL: 5–10m, background refresh, key eviction on `kid` miss.
- Revocation cache: short TTL (e.g., 5m) with negative caching; refresh on revoke events broadcast (pub/sub).
- User state cache: optional `user_version` tag in token; mismatch forces lookup to reject disabled users quickly.

### 6.1 Cache sources & failover
- JWKS: memory cache with async refresh; on cache miss and JWKS endpoint failure → fail-closed (config flag for emergency fail-open).
- Revocation: in-memory LRU (TTL 5m) backed by shared store (Redis/etcd) with TTL at least equal to refresh TTL; if backing store unavailable, default fail-closed for admin actions and configurable for user traffic.
- User directory: optional memory cache keyed by `sub` + version; on mismatch fetch from primary DB; on DB outage, deny with `UNAVAILABLE` unless emergency flag is set.

## 7. Revocation Semantics
- Access tokens short-lived (≈15m) to bound risk.
- Refresh tokens longer (≈7d) and always revocable; store refresh `jti` in revocation set on logout/password change/disable.
- Optional access `jti` revocation for urgent kills; keep set size bounded with TTL.

### 7.1 Revocation store
- Primary: shared KV (Redis/etcd) storing revoked `jti` with TTL = remaining token lifetime; include `user_state_version` to catch disables.
- Secondary: node-local LRU to avoid hot-looping on the KV; populate on first miss; eviction by TTL/LRU.
- Broadcast: publish revoke events over pub/sub to warm peers; peers write to local cache immediately.
- Failure mode: if shared KV unreachable, deny refresh-token flows and optionally allow access tokens only if still within a short grace window (feature-flagged emergency mode); emit alert.

### 7.2 Refresh rotation semantics
- On each refresh, issue new access + new refresh with a new `jti`; keep the old refresh valid for a short overlap window (e.g., 2–5 minutes) to tolerate network retries.
- After the overlap, revoke the old refresh in the shared KV and local cache; access tokens tied to the old refresh expire naturally.
- Concurrent refreshes: first success wins; later attempts with an already-rotated token return `PERMISSION_DENIED`/`TOKEN_REVOKED`.

## 8. Failure & Degradation
- JWKS fetch fail: reject tokens; do not accept on stale key unless feature-flagged emergency mode.
- Cache miss with backend down: fail-closed by default. Emergency flag could allow fail-open briefly with alerting.
- Clock skew: allow ±120s; outside window → reject.
- Malformed token or alg mismatch → reject and log sampled details (no secrets).

### 8.1 Key compromise (pre-rotation stopgap)
- Until automated rotation exists, on suspected key compromise: immediately stop issuing new tokens, mark affected `kid` as blocked (deny validation), and require re-login after new key deployment; publish incident notice to all nodes; purge refresh tokens by revoking all with the compromised `kid`.

### 8.2 Emergency mode governance
- Emergency fail-open flags must be time-bounded (default 15 minutes) and audited (actor, reason, expiry). Auto-revert when TTL expires.

## 9. Observability
- Metrics: auth success/fail counts by reason, JWKS fetch latency/error, revocation hit/miss, skew rejects.
- Logs: sampled invalid-token reasons, `kid` misses, revocation checks; redact token payloads.
- Tracing: span tag `auth.result` (ok/deny/reason), `kid`, `revocation.checked`.

## 10. Security & Ops
- HTTPS termination required; consider mTLS between gateways and auth service.
- Key rotation: new `kid` published to JWKS; dual-publish until old tokens expire; alert on unknown `kid` spikes.
- Pen testing focus: JWT alg confusion, `kid` injection, cache poisoning; strict parser and allowlist algs.

## 11. Key Rotation Workflow (Deferred)
Key rotation is **not implemented in this phase**. When picked up:
1) Generate new keypair; assign new `kid`.
2) Publish JWKS containing old+new keys; start issuing tokens with the new `kid`.
3) Monitor for unknown `kid`/signature failures; ensure nodes refreshed JWKS.
4) After access-token TTL + grace window, stop issuing with old key; remove old key from JWKS.
5) Optionally revoke outstanding refresh tokens if rotating due to compromise.
CLI/Admin path (future): handled via the console/CLI in [discussion/008_console.md](discussion/008_console.md).

## 12. Request Context for Rules
- Populate for every authorized request: `request.auth.uid`, `request.auth.username`, `request.auth.roles` (optional), `request.time`, `request.resource.data` for writes.
- For reads, fetch `resource.data` of the target document when required by the matched rule.

## 13. Rule Evaluation (Firestore-like)
- Language: CEL subset (see 004), helpers: `get(path)`, `exists(path)`, access to `request`/`resource`.
- Lifecycle: match rule by path → pre-fetch documents for `get()` → evaluate CEL → deny on `false` or evaluation error → on allow, forward to Query Engine.
- Strictness: deny-by-default; missing rule → deny; evaluation error → deny.
- Index enforcement: queries needing missing indexes return `MISSING_INDEX` (per 002) before rule evaluation to avoid wasted work.

## 14. Control Plane for Rules (Minimal)
- Artifact: versioned rule file (e.g., `security.rules.cel`).
- Publish flow: `syntrix-cli rules push` → server validates CEL syntax → stages with version → activate with atomic swap; keep last N versions; support `rules rollback <version>`.
- AuthZ: only `role=admin` may push/rollback; enforced via admin endpoint.
- Audit: record who pushed/rolled back rules, version, timestamp, diff summary.
- Full publish/rollback UX lives in [discussion/008_console.md](discussion/008_console.md).

## 15. API Surface (Auth)
- `POST /v1/auth/signup` — username, password; returns access (and refresh if enabled).
- `POST /v1/auth/login` — username, password; returns access (+ refresh optional).
- `POST /v1/auth/logout` — refresh token (or Authorization header); revokes tokens.
- `POST /v1/auth/refresh` — refresh token; returns new access (+ refresh).
- `POST /v1/auth/password` — old_password, new_password; rotates tokens.

## 16. Operational Concerns
- Rate limiting: per-IP and per-username for sign-up/login; low default ceilings.
- Lockout: configurable temporary lock after repeated failures; avoid permanent lockouts.
- Transport security: HTTPS only; HSTS recommended.
- Secrets: store private keys in secret manager; restrict access to auth service; rotate regularly.
- Backups: include user table and rule versions in regular backups; test restore.
- Metrics: auth success/fail counts, latency, revocation hits, rule evaluation allow/deny counts, index-miss errors.

### 16.1 Pagination & auditing defaults
- Admin list APIs: require pagination (`page_size` default 50, max 200) and sort order; filter by username/role/status; return opaque cursors.
- Audit log retention: minimum 30 days; redact secrets; include actor, action, target, result, client IP/UA where available.

## 17. Migration / Future
- Add external IdP (OIDC/SAML) as optional provider; unify claims mapping into `request.auth`.
- Add email/SMS password reset and MFA.
- Introduce tenant/project model; scope rules and keys per tenant.
- Move revocation to short-lived access tokens only (no refresh) for simplified ops if acceptable UX-wise.

## 18. Open Items / Next Deep Dives
- Index management and composite index lifecycle (not covered here).
- Password reset/MFA/IdP federation (explicitly out-of-scope for current milestone).
- Tenant/project model and per-tenant key/rule isolation.
