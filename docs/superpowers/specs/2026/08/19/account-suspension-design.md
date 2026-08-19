# Account Suspension Design

Date: 2026-08-19
Status: pending review
Branch: `feat/account-suspension`

## Background and Goals

We need to support suspending a person's account: once suspended, the user can no longer log in to Multica; sessions that are already logged in become invalid on their next request, and the frontend automatically returns to the login page.

Upstream `multica-ai/multica` has a matching design that was never merged:

- **Issue #1688** (open): the v1 plan — add an `account_status` column to the `"user"` table (`active`/`suspended`); when `suspended`, every auth path uniformly returns **403 + the stable error code `ACCOUNT_SUSPENDED`**; session invalidation works by having the middleware re-validate the user's status on every request (this project uses stateless JWTs — no sessions table, no revocation mechanism). Soft delete and email-uniqueness changes are deferred to v2.
- **PR #1689** (open, stalled): fully implemented #1688 and the reviewer gave "approve with clarifications", but after main merged the Redis PAT cache (10-minute TTL) a must-fix regression appeared — **the PAT cache-hit path bypasses the suspension check**, so a suspended user's PAT keeps working for up to 10 minutes; the author never followed up. The reviewer also noted: already-established WebSocket connections are not kicked, only new connections are rejected.

This design aligns fully with upstream in shape (column name, status values, error code, centralized helper), so a future upstream merge of any descendant of that work costs close to nothing, and it additionally fixes the two gaps that stalled the upstream PR (PAT cache bypass, no WS kick).

## Current State (code audit conclusions)

- Auth: stateless JWT (HS256, cookie `multica_auth`, default 30-day TTL); login methods are email verification code + Google OAuth, no passwords. `POST /auth/logout` only clears the cookie — no server-side revocation.
- Session validation: `middleware.Auth` (`server/internal/middleware/auth.go`) and `middleware.DaemonAuth`; credential types include JWT, `mul_` PAT, `mat_` task token, `mdt_` daemon token, `mcn_` cloud PAT. The JWT path currently performs zero DB reads.
- The `"user"` table has no status/disabled column of any kind. However, a hardcoded emergency denylist already exists at `server/internal/auth/temporary_disabled_users.go`, whose comment explicitly says "Remove this once account suspension is persisted and enforced from the user model." It has checkpoints planted at the following locations, all returning 403 `"account disabled"`:
  - `middleware/auth.go` — all 5 auth paths (task_token / cloud_pat / pat_cache / pat / jwt)
  - `middleware/daemon_auth.go` — 4 sites
  - `realtime/hub.go` `authenticateToken` — 2 sites
  - `handler/auth.go`: `issueJWT`, `findOrCreateUser`, `SendCode`, `VerifyCode`, Google login, `IssueCliToken`
- Permission model: only workspace-level `member.role` (owner/admin/member) — **there is no global admin concept**.
- Frontend 401/403 handling: `packages/core/api/client.ts` calls `handleUnauthorized` only on 401; web/desktop's `onUnauthorized` (`packages/core/platform/core-provider.tsx`) only clears the token without navigating, and the actual redirect to the login page relies on route guards seeing `user === null` (which only happens when the boot-time `getMe()` returns 401). **Existing 403s trigger no logout/redirect at all.** Mobile (`apps/mobile/app/_layout.tsx`) has a complete logout+redirect implementation to use as a reference.
- WebSocket: validated only at connect time; the Hub has no per-user disconnect API; the client (`ws-client.ts`) does not recognize `auth_error` and reconnects indefinitely when the connection is rejected.
- Caches: `auth.AuthCacheTTL = 10min`, shared by `PATCache` / `DaemonTokenCache`; there is also a `MembershipCache`.
- The convergence pattern for member removal: `revokeAndRemoveMember` (`server/internal/handler/workspace_revoke.go`) — cancels in-flight tasks, force-offlines runtimes, deletes daemon tokens, invalidates caches.

## Confirmed Design Decisions

| Decision | Conclusion |
| --- | --- |
| Permission model | `ADMIN_EMAILS` env var configures system admins (comma-separated emails); only system admins can suspend/restore; an admin cannot suspend themselves |
| Existing WS connections | Kicked immediately on suspension (new `Hub.DisconnectUser`) |
| Admin UI | Built in this iteration: a new global `/admin` page (pre-workspace single-word route, per routing rules) |
| Upstream alignment | Column `account_status`, values `active`/`suspended`, error code `ACCOUNT_SUSPENDED`, centralized `auth.UserMayAuthenticate` |

## Design

### Database

- Migration: `ALTER TABLE "user" ADD COLUMN account_status TEXT NOT NULL DEFAULT 'active'` + `CHECK (account_status IN ('active','suspended'))`.
- No indexes, no foreign keys, no cascades — per the repo migration rules (a plain column-add needs no CONCURRENTLY).
- `server/pkg/db/queries/user.sql` gains read-status-by-id and update-status-by-id queries; regenerate with `make sqlc` (local machine needs sqlc v1.31.1).

### Backend Enforcement

1. **Centralized check**: new `server/internal/auth/account.go`:
   - `UserMayAuthenticate(status string) error`: only `active` passes; `suspended`, empty, and unknown values are all rejected (fail-closed).
   - `WriteAccountSuspendedResponse(w)`: uniformly writes `403` + `{"error":"account suspended","code":"ACCOUNT_SUSPENDED"}`.
2. **Replace the emergency denylist**: delete `temporary_disabled_users.go` and replace every existing call site in place with the DB-status-based check (see the checkpoint inventory under "Current State").
3. **Per-request effect and caching**:
   - The JWT path gains a per-userID account-status lookup, backed by a Redis cache (reusing `auth.AuthCacheTTL`, 10 minutes), named e.g. `AccountStatusCache`.
   - **Suspend/restore operations must actively invalidate by userID**: `AccountStatusCache` + `PATCache` + `DaemonTokenCache`, guaranteeing "invalid on the next request". This is the fix for the regression that stalled upstream PR #1689 (PAT cache hits bypassing the check): cache-hit paths also run the status check, and the write path invalidates proactively.
4. **Admin API** (new route group, protected by a system-admin check):
   - `GET /api/admin/users`: lists all users with `account_status`.
   - `PATCH /api/admin/users/{id}/status`: body `{"status":"active"|"suspended"}`; path UUID via `parseUUIDOrBadRequest`; operating on yourself is forbidden.
   - System-admin determination: requesting user's email ∈ `ADMIN_EMAILS` (read from server config, case-insensitive comparison).
   - `GET /api/me` response gains `is_system_admin: boolean` for the frontend to show/hide the entry point.
5. **Convergence on suspension** (single transaction / sequential execution, modeled on `revokeAndRemoveMember`):
   - Update `account_status = 'suspended'`;
   - Cancel the user's in-flight tasks and force-offline their runtimes;
   - Invalidate the three cache types above;
   - Call `Hub.DisconnectUser(userID)` to sever all of their WS connections (send an `auth_error` frame, then close).
   - Restore (`active`) only updates the status and invalidates caches — no convergence actions.

### WebSocket

- The `Hub` maintains a userID → connections index (or walks the existing connection table) and gains `DisconnectUser(userID)`.
- Connect-time validation keeps the existing `authenticateToken` checkpoints (they take effect automatically once replaced with the DB status check).
- Client `packages/core/api/ws-client.ts`: recognize `auth_error` frames and rejected upgrades (403), stop reconnecting, and trigger the session-termination flow (current behavior is infinite reconnects with no user-visible signal).

### Frontend (web / desktop shared + mobile)

1. **Session-termination flow**:
   - `packages/core/api/client.ts`: beside the existing 401 handling, add a 403 + `code === "ACCOUNT_SUSPENDED"` branch that also goes through `handleUnauthorized` (carrying a suspended-reason marker).
   - Complete web/desktop's `onUnauthorized` (`core-provider.tsx`): clear the auth store user + clear the token + navigate to the login page (modeled on mobile's complete `_layout.tsx` implementation, including idempotency protection against loops).
   - The login page shows an "account suspended, contact your administrator" notice based on the marker.
   - Mobile's independent client (`apps/mobile/data/api.ts`) gains the same `ACCOUNT_SUSPENDED` branch.
2. **Login rejection**: when the verification-code / Google login endpoints return `ACCOUNT_SUSPENDED`, the login form shows the suspension notice in place (not the generic error copy).
3. **Admin page `/admin`**:
   - Page and components live in `packages/views/admin/`; web (`apps/web/app/admin/`) and desktop wire up their own routing.
   - Only `is_system_admin === true` shows the entry and allows access; the server remains the final authority.
   - Features: user list (name, email, status, joined date), suspend/restore actions with confirmation dialogs; suspended users are clearly marked; your own row shows no suspend action.
   - API responses parsed through zod schemas + `parseWithFallback`; the UI optional-chains fields defensively; copy (Chinese and English) follows `apps/docs/content/docs/developers/conventions*.mdx`.

### Compatibility and Security Details

- The 403 response body carries a stable `code` field; older clients that don't recognize the code see an ordinary 403 error and are never falsely logged out — consistent with the API compatibility rules (never pin a single boolean, use explicit checks).
- Known residual window (same as upstream, documenting is sufficient): already-issued CDN signed cookies / presigned URLs remain valid within their TTL.
- When `ADMIN_EMAILS` is unconfigured there are no system admins, every admin API returns 403, and the `/admin` entry is invisible — off by default. The existence of any admin depends on this setting; be sure to configure it at deployment.
- Residual window: a daemon that authenticates with a PAT (`mul_`) is blocked per-request; `mdt_` daemon tokens carry no user identity, so suspension deletes the daemon tokens for the daemons on the user's runtimes in the same transaction as the status flip (mirroring `revokeAndRemoveMember`) AND severs their already-established daemon WebSockets (`daemonws.Hub.DisconnectRuntimes`) — a deleted token only gates new connections.
- Suspension's DB-side convergence (status flip, task cancellation, runtime force-offline, daemon-token deletion) runs in ONE transaction: a 200 response never reports a suspension whose convergence half-failed. Cache invalidation, broadcasts, and WS kicks run post-commit as best-effort side effects.
- Multi-node deployments: the suspend path publishes control frames through the relay and every node enforces SERVER-SIDE — user sockets via `fanoutUser` intercepting the suspended `auth_error` frame and evicting (not relying on the client honoring it), daemon sockets via the `daemon:runtimes_revoked` control frame that each node's daemon hub interprets as a local `DisconnectRuntimes`. Cooperating clients additionally receive the frame first and terminate their own session.
- Cache revocation runs BEFORE the transaction commits: if Redis refuses the deletes the whole change rolls back and the endpoint returns 500 with nothing changed, so the admin's retry re-runs the full convergence — including re-deriving the daemon-token hashes from the not-yet-deleted rows. A post-commit second pass closes the small re-population race; its account-entry failure is surfaced (retryable), while daemon-token entries there are best-effort (already cleared pre-commit; a re-cached entry expires within AuthCacheTTL).
- Live-connection revocation failures are surfaced too: the relay publish that tells other nodes to kick user/daemon sockets returns its error, and the endpoint reports 500 (`live-connection revocation failed; retry`) instead of a false success — user ID and runtime IDs are re-derivable, so the idempotent retry re-runs the kicks.
- Suspension control frames are matched STRUCTURALLY on delivery (the relay's event-id injection re-encodes frames, so byte equality never holds cross-node), and an in-flight daemon heartbeat that passed auth before the suspension re-checks the owner's account status before writing liveness — it cannot resurrect a force-offlined runtime.

## Test Plan

Per the repo's test layering rules, behavioral changes get a failing test first (TDD):

- **Go (`server/`)**:
  - `UserMayAuthenticate` unit tests: active passes; suspended / empty / unknown values rejected (fail-closed).
  - Integration tests: after suspension — verification-code login rejected, an existing JWT's next request returns 403 + `ACCOUNT_SUSPENDED`, PAT (including the cache-hit path) returns 403 on the next request, the daemon-token path returns 403, `IssueCliToken` rejected.
  - Cache-invalidation immediacy: a request immediately after suspension (cache not yet expired) is still rejected.
  - Admin API: non-admin gets 403, an admin can suspend/restore, cannot suspend themselves, `parseUUIDOrBadRequest` boundaries.
  - `Hub.DisconnectUser`: connections severed after suspension, new connections rejected.
- **TS**:
  - `packages/core`: admin API schema parsing + malformed-response tests; client 403 + `ACCOUNT_SUSPENDED` triggers session termination; ws-client stops reconnecting on `auth_error`.
  - `packages/views`: admin page component tests (list rendering, confirmation dialog, self not suspendable, suspended-state marking).
  - `apps/web` / desktop: logout-redirect wiring tests (if necessary).
- Verification commands: `make test`, `pnpm typecheck`, `pnpm --filter @multica/core test`, `pnpm --filter @multica/views test`.

## Explicitly Out of Scope (this iteration)

- Soft delete (`deleted_at`) and email-uniqueness changes (upstream's v2 scope).
- A suspension-reason field and audit logging.
- Immediate revocation of already-issued CDN signed credentials.
- Any new semantics beyond the workspace-level "remove member only" (the existing `revokeAndRemoveMember` is unchanged).
