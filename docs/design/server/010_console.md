# User & Admin Console

**Date:** December 18, 2025
**Topic:** Web console for end-users and admins

## 1. Scope

- End-user portal: authenticate, view/manage own data (per rules), no access to others' data.
- Admin portal: all end-user capabilities plus rule and user administration.
- Single app with role-based surfaces; respects deny-by-default rules.

## 2. Capabilities

### End-user

- Auth: login via JWT (same auth service as core), respect rate limits/lockout defined in identity doc.
- Data: list/read/update documents the user is allowed to access; operations routed through normal API with rule checks.
- Profile: view/update own profile data (non-secret fields only).

### Admin (in addition to above)

- Rules: list versions, dry-run upload, activate staged, rollback.
- Users: list, create, enable/disable, admin-rotate password.
- Health/audit: view admin audit log, rule generation lag, JWKS fetch errors (read-only).
- Note: key rotation UI deferred (per 007_2).

### 2.1 Safety defaults

- Serve admin surface on a distinct domain/subdomain; enforce HTTPS + optional mTLS for admin routes.
- Default IP allowlist for admin endpoints; user-facing surface remains public with rate limits.
- Hide admin controls fully for non-admins; avoid client-side-only gating for sensitive actions.
- All API calls use Authorization header (no cookies); enforce CORS to the allowed origins set.

### 2.2 Admin session handling

- Admin access tokens should be short-lived; prefer no refresh tokens in the browser. If refresh is required, keep it in memory (not localStorage) and rotate with overlap window per 7.2 of identity doc.
- Idle timeout (e.g., 15–30 minutes) with explicit re-auth; display countdown to avoid surprise logouts.
- Distinct CORS allowlists for admin vs end-user origins; update via config, not UI.

#### Rule publish/rollback (from 007_3)

- Artifact: text file (e.g., `security.rules.cel`), versioned in control-plane table `rules_versions` with `id`, `createdAt`, `created_by`, `status` (staged/active/disabled), `hash`, `size`, `notes`; keep last N (e.g., 20).
- Serving: control-plane exposes `/admin/rules/active` and watch/poll endpoint for nodes to fetch active version & generation.
- Publish flow: `syntrix-cli rules push` → parse/compile/validate (forbidden functions, size limits) → optional `dry-run` (stage only) → activate via atomic pointer swap → notify nodes to hot-reload generation.
- Rollback: `syntrix-cli rules rollback <version>`; swap pointer, audit `rolled_back_from`.
- Enforcement: nodes carry `rules_version`; on mismatch, fetch latest; fail-closed on reload failure (keep prior active, alert).
- AuthZ/Audit: admin-only actions; audit who/when/action/versions/hash/notes/validation result; two-person approval not in scope now.
- Safety/observability: metrics for reload success/fail, generation lag, validation errors; size cap (e.g., 256 KB), depth cap, forbid unbounded `get()`; dry-run encouraged.
- Reload failure handling: nodes continue serving the last known good version, mark themselves degraded, emit alert/metric; block activation if validation/hydration fails.
- Idempotency: all `push`/`rollback` calls require `Idempotency-Key`; duplicate keys are no-ops returning prior result within a 24h window.

#### User data UX and auth-state issues

- If rule reload fails, surface warning only to admins; end-users see generic failure without leaking rule state.
- For partially degraded state (stale rules), include banner in admin console linking to diagnostics.

#### Admin CLI & API (from 007_5)

- Scope: CLI surface for auth user ops and rules management; admin HTTP endpoints secured by admin JWT and optional mTLS.
- CLI commands: `syntrix-cli rules push <file> [--dry-run] [--note "..."]`, `rollback <version>`, `list`; `auth create-user`, `disable-user`, `enable-user`, `rotate-password`, `list-users` with filters.
- Admin API: `POST /admin/rules/push`, `POST /admin/rules/rollback`, `GET /admin/rules`; `POST /admin/users`, `PATCH /admin/users/{username}`, `GET /admin/users` with filters.
- AuthN/AuthZ: require admin JWT with `role=admin`, validate via standard token pipeline; optional mTLS to bind audit to client cert; rate-limit admin actions.
- Idempotency & safety: use `Idempotency-Key` for user mutations; reject weak passwords; enforce username uniqueness; prevent disabling the last admin.
- Audit & observability: audit who/when/action/target/diff/client cert; metrics for admin API success/fail, latency, rule push/rollback, user mutation counts.
- Not in scope: interactive TUI, org/tenant hierarchy management, API keys, signed URLs, secret rotation APIs.

### Pagination & rate limits

- All list endpoints (rules, users, audit logs) require pagination with opaque cursors; default `page_size` 50, max 200.
- Support filters: rules (status, creator, version id), users (status, role, username prefix), audit logs (action, actor, date range).
- Enforce per-IP and per-actor rate limits for admin APIs; burst credits but low sustained ceilings.
- Large exports are out of scope; encourage filtered pagination instead of bulk dumps.

### Audit & error semantics

- Audit record shape (minimum): `actor`, `action`, `target`, `result`, `reason`, `request_id`, `client_cert_hash` (if present), `ip`, `user_agent`, `timestamp`.
- Error codes: prefer `409 Conflict` for concurrent mutations/Idempotency-Key clashes, `422 Unprocessable Entity` for validation errors, `429 Too Many Requests` for rate limits, `403` for authz, `401` for authn failures.

## 3. Architecture

- Single SPA (e.g., React) with role-based feature flags.
- Backend: reuse public API for data access; reuse admin API for admin ops; served over HTTPS.
- AuthZ: JWT with roles (`admin`), gateway enforces; optional mTLS for admin actions.
- CSRF: token in Authorization header; no cookies; CORS locked down.

## 4. Endpoints Used

- User data: standard `/api/v1/...` CRUD/query; subject to rules.
- Auth: `/auth/v1/login`, `/auth/v1/logout`, `/auth/v1/refresh`, `/auth/v1/password`.
- Admin (admins only): `/admin/rules` (list/push/rollback), `/admin/users` (list/create/patch), `/admin/health`.

## 5. UX & Safety

- End-users never see secrets or password_hash; only allowed fields per rules.
- Admin actions require confirmation; dry-run before activate; pagination and filters for lists.
- Clear role indicator in UI; hide admin controls for non-admins.

## 6. Security & Ops

- HTTPS only; consider separate domain/subdomain for admin entry; optional IP allowlist for admin.
- Rate limit admin mutations; audit all admin actions (who/when/what/result, client cert if present).
- Size limits aligned with rule upload caps (e.g., 256 KB). No secret storage in UI.

## 7. Future

- SSO/IdP integration for both user and admin.
- Tenant-aware views once multi-tenant exists.
- Key rotation UI when implemented.
- Notifications/alerts surface for admins (rule load failures, unknown kid spikes).
