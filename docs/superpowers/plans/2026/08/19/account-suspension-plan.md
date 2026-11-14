# Account Suspension Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A system admin can suspend a user account; suspended users cannot log in, every existing credential (JWT/PAT/daemon/WS) is rejected on the next request with `403 ACCOUNT_SUSPENDED`, and logged-in clients drop to the login page.

**Architecture:** Add `account_status` to `"user"` (aligned with upstream multica-ai/multica #1688/#1689: values `active`/`suspended`, error code `ACCOUNT_SUSPENDED`, fail-closed). Replace the in-memory emergency denylist (`server/internal/auth/temporary_disabled_users.go`) with a DB-backed check behind a Redis cache (`AuthCacheTTL`), invalidated on suspend so revocation is immediate — this closes the PAT-cache-bypass regression that stalled upstream PR #1689. System admins come from an `ADMIN_EMAILS` env allowlist; a new global `/admin` page (design approved, see the design canvas) manages users. Frontends treat `403 + code=ACCOUNT_SUSPENDED` as session death: logout + redirect to login with a "账号已被禁用" notice; the WS client stops reconnecting on `auth_error`.

**Tech Stack:** Go (chi, sqlc, gorilla/websocket, go-redis), PostgreSQL, TypeScript (zod, TanStack Query, Zustand), Next.js (web), React Router (desktop), Expo (mobile).

**Spec:** `docs/superpowers/specs/2026/08/19/account-suspension-design.md`

## Global Constraints

- Status values are exactly `active` and `suspended`; the machine-readable error code is exactly `ACCOUNT_SUSPENDED`; the error message string is exactly `account suspended`. Unknown/empty status is rejected (fail-closed).
- Migrations: no FK/cascades; the column-add needs no index. sqlc regen requires **sqlc v1.31.1** (local v1.29.0 downgrades the tree — verify `sqlc version` first).
- All code comments in English. Chinese UI copy follows `apps/docs/content/docs/developers/conventions.zh.mdx` (Member=成员, Account=账号).
- Frontend: parse API JSON via `parseWithFallback` + zod (never cast), explicit `=== true` checks on server booleans, `default` branch on server enums, malformed-response test for every new/changed endpoint schema.
- Tests: TDD (write the failing test first). Locations per CLAUDE.md table. `.test.ts` with no DOM needs `// @vitest-environment node`. Never resolve real agent CLIs in tests.
- Commits: conventional prefixes, atomic per task. Do not push until the end.
- The repo's fork remote is `origin` (furtherref/multica); never name internal hosts in code/tests.
- Run `pnpm --filter @multica/views test` (full package) before finishing if any shared component changed.

---

### Task 1: DB migration + sqlc queries

**Files:**
- Create: `server/migrations/342_user_account_status.up.sql`
- Create: `server/migrations/342_user_account_status.down.sql`
- Modify: `server/pkg/db/queries/user.sql`
- Modify: `server/pkg/db/queries/agent_runtime.sql` (or wherever `ListAgentRuntimesByOwner` lives — grep for it; add the all-workspaces variant beside it)

Note: 342 assumed from current tail `341_issue_property_actor_types`; use the next free number at implementation time.

**Interfaces:**
- Produces (after `make sqlc`): `db.User.AccountStatus string`; `queries.GetUserAccountStatus(ctx, id pgtype.UUID) (string, error)`; `queries.SetUserAccountStatus(ctx, db.SetUserAccountStatusParams{ID, AccountStatus}) (db.User, error)`; `queries.ListUsersWithStatus(ctx) ([]db.User, error)`; `queries.ListAgentRuntimesByOwnerAllWorkspaces(ctx, ownerID pgtype.UUID) ([]db.AgentRuntime, error)`.

- [ ] **Step 1: Write the migration**

`342_user_account_status.up.sql`:
```sql
ALTER TABLE "user"
    ADD COLUMN account_status TEXT NOT NULL DEFAULT 'active'
    CHECK (account_status IN ('active', 'suspended'));
```

`342_user_account_status.down.sql`:
```sql
ALTER TABLE "user" DROP COLUMN account_status;
```

- [ ] **Step 2: Add queries**

Append to `server/pkg/db/queries/user.sql`:
```sql
-- name: GetUserAccountStatus :one
SELECT account_status FROM "user"
WHERE id = $1;

-- name: SetUserAccountStatus :one
UPDATE "user" SET
    account_status = $2,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: ListUsersWithStatus :many
-- Global user list for the system-admin console. Not workspace-scoped on
-- purpose: suspension is an account-level property.
SELECT * FROM "user"
ORDER BY created_at ASC;
```

Beside the existing `ListAgentRuntimesByOwner` (copy its exact FROM/SELECT shape, drop the workspace filter):
```sql
-- name: ListAgentRuntimesByOwnerAllWorkspaces :many
-- All runtimes a user owns across every workspace. Used by account
-- suspension to cancel in-flight tasks and force runtimes offline.
SELECT * FROM agent_runtime
WHERE owner_id = $1;
```
(Verify the real table/column names against the existing query before committing.)

- [ ] **Step 3: Regenerate and verify**

Run: `sqlc version` (must be v1.31.1), then `make sqlc`, then `(cd server && go build ./...)`.
Expected: build passes; `git diff --stat server/pkg/db/generated` shows only additions related to the new queries + `AccountStatus` field.

- [ ] **Step 4: Commit**

```bash
git add server/migrations/342_user_account_status.* server/pkg/db/queries/ server/pkg/db/generated/
git commit -m "feat(server): add user.account_status column and admin queries"
```

---

### Task 2: `auth.UserMayAuthenticate` + constants

**Files:**
- Create: `server/internal/auth/account.go`
- Test: `server/internal/auth/account_test.go`

**Interfaces:**
- Produces: `auth.AccountStatusActive = "active"`, `auth.AccountStatusSuspended = "suspended"`, `auth.AccountSuspendedMessage = "account suspended"`, `auth.AccountSuspendedCode = "ACCOUNT_SUSPENDED"`, `auth.ErrAccountSuspended error`, `auth.UserMayAuthenticate(status string) error`.

- [ ] **Step 1: Write the failing test** (`account_test.go`)

```go
package auth

import (
	"errors"
	"testing"
)

func TestUserMayAuthenticate(t *testing.T) {
	cases := []struct {
		status string
		wantOK bool
	}{
		{"active", true},
		{"suspended", false},
		{"", false},        // fail-closed: empty status never authenticates
		{"pending", false}, // fail-closed: unknown status never authenticates
		{"ACTIVE", false},  // exact match only; DB CHECK guarantees lowercase
	}
	for _, tc := range cases {
		err := UserMayAuthenticate(tc.status)
		if tc.wantOK && err != nil {
			t.Errorf("UserMayAuthenticate(%q) = %v, want nil", tc.status, err)
		}
		if !tc.wantOK && !errors.Is(err, ErrAccountSuspended) {
			t.Errorf("UserMayAuthenticate(%q) = %v, want ErrAccountSuspended", tc.status, err)
		}
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `(cd server && go test ./internal/auth -run TestUserMayAuthenticate -v)`
Expected: FAIL (undefined: UserMayAuthenticate)

- [ ] **Step 3: Implement** (`account.go`)

```go
package auth

import "errors"

// Account status values persisted in "user".account_status. Aligned with
// upstream multica-ai/multica#1688 so a future upstream merge is a no-op.
const (
	AccountStatusActive    = "active"
	AccountStatusSuspended = "suspended"
)

// AccountSuspendedMessage is the human-readable error; AccountSuspendedCode
// is the stable machine-readable code clients branch on.
const (
	AccountSuspendedMessage = "account suspended"
	AccountSuspendedCode    = "ACCOUNT_SUSPENDED"
)

var ErrAccountSuspended = errors.New(AccountSuspendedMessage)

// UserMayAuthenticate returns nil only for an explicitly active account.
// Every other value — suspended, empty, unknown — is rejected (fail-closed):
// a status this code does not recognize must never widen access.
func UserMayAuthenticate(status string) error {
	if status == AccountStatusActive {
		return nil
	}
	return ErrAccountSuspended
}
```

- [ ] **Step 4: Run test**

Run: `(cd server && go test ./internal/auth -run TestUserMayAuthenticate -v)`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add server/internal/auth/account.go server/internal/auth/account_test.go
git commit -m "feat(auth): fail-closed account status rule"
```

---

### Task 3: AccountStatusCache + AccountGuard

**Files:**
- Create: `server/internal/auth/account_cache.go` (cache; mirror `pat_cache.go` exactly)
- Modify: `server/internal/auth/account.go` (add AccountGuard)
- Test: `server/internal/auth/account_guard_test.go`

**Interfaces:**
- Consumes: `AuthCacheTTL` from `pat_cache.go`; `db` generated `GetUserAccountStatus`.
- Produces:
  - `auth.AccountStatusCache` with `NewAccountStatusCache(rdb *redis.Client) *AccountStatusCache`, `Get(ctx, userID string) (status string, ok bool)`, `Set(ctx, userID, status string)`, `Invalidate(ctx, userID string)`. Redis key prefix `mul:auth:acct:`; TTL always `AuthCacheTTL`; nil receiver is a safe no-op (copy PATCache's nil-safety pattern verbatim).
  - `auth.AccountStatusQuerier interface { GetUserAccountStatus(ctx context.Context, id pgtype.UUID) (string, error) }` (satisfied by `*db.Queries`).
  - `auth.AccountGuard struct { Queries AccountStatusQuerier; Cache *AccountStatusCache }` with `func (g *AccountGuard) Check(ctx context.Context, userID string) error` — nil guard or nil Queries returns nil (feature off, e.g. minimal test routers); cache hit → `UserMayAuthenticate(cached)`; miss → DB lookup, `Set`, `UserMayAuthenticate`. DB error where the user row is missing → `ErrAccountSuspended` (a deleted account must not authenticate); other DB errors → return the error (caller maps to 503). Invalid userID string → `ErrAccountSuspended`.

- [ ] **Step 1: Write the failing test** (`account_guard_test.go`; pure unit test, fake querier, nil cache)

```go
package auth

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type fakeStatusQuerier struct {
	status string
	err    error
}

func (f *fakeStatusQuerier) GetUserAccountStatus(_ context.Context, _ pgtype.UUID) (string, error) {
	return f.status, f.err
}

func TestAccountGuardCheck(t *testing.T) {
	const uid = "3f0e1f6a-8f3f-4a5e-9b6a-2f8f0f1a2b3c"
	ctx := context.Background()

	t.Run("nil guard allows", func(t *testing.T) {
		var g *AccountGuard
		if err := g.Check(ctx, uid); err != nil {
			t.Fatalf("nil guard: %v", err)
		}
	})
	t.Run("active allows", func(t *testing.T) {
		g := &AccountGuard{Queries: &fakeStatusQuerier{status: AccountStatusActive}}
		if err := g.Check(ctx, uid); err != nil {
			t.Fatalf("active: %v", err)
		}
	})
	t.Run("suspended rejects", func(t *testing.T) {
		g := &AccountGuard{Queries: &fakeStatusQuerier{status: AccountStatusSuspended}}
		if !errors.Is(g.Check(ctx, uid), ErrAccountSuspended) {
			t.Fatal("want ErrAccountSuspended")
		}
	})
	t.Run("missing user rejects", func(t *testing.T) {
		g := &AccountGuard{Queries: &fakeStatusQuerier{err: pgx.ErrNoRows}}
		if !errors.Is(g.Check(ctx, uid), ErrAccountSuspended) {
			t.Fatal("want ErrAccountSuspended for deleted user")
		}
	})
	t.Run("invalid uuid rejects", func(t *testing.T) {
		g := &AccountGuard{Queries: &fakeStatusQuerier{status: AccountStatusActive}}
		if !errors.Is(g.Check(ctx, "not-a-uuid"), ErrAccountSuspended) {
			t.Fatal("want ErrAccountSuspended for bad uuid")
		}
	})
	t.Run("transient db error propagates", func(t *testing.T) {
		boom := errors.New("boom")
		g := &AccountGuard{Queries: &fakeStatusQuerier{err: boom}}
		if !errors.Is(g.Check(ctx, uid), boom) {
			t.Fatal("want transient error propagated")
		}
	})
}
```

- [ ] **Step 2: Run to verify failure**

Run: `(cd server && go test ./internal/auth -run TestAccountGuardCheck -v)`
Expected: FAIL (undefined: AccountGuard)

- [ ] **Step 3: Implement**

`account_cache.go`: copy `pat_cache.go`'s structure (nil-safety, swallowed Redis errors, slog warns) with prefix `mul:auth:acct:`, value = status string, fixed `AuthCacheTTL` (statuses don't expire like PATs, no TTLForExpiry needed).

Append to `account.go`:
```go
// AccountStatusQuerier is the single sqlc method AccountGuard needs;
// *db.Queries satisfies it.
type AccountStatusQuerier interface {
	GetUserAccountStatus(ctx context.Context, id pgtype.UUID) (string, error)
}

// AccountGuard answers "may this user authenticate right now" from the
// account_status column, fronted by a short-TTL Redis cache. Suspension
// invalidates the cache entry, so revocation is immediate — including on
// the PAT-cache-hit path (the regression that stalled upstream #1689).
type AccountGuard struct {
	Queries AccountStatusQuerier
	Cache   *AccountStatusCache
}

// Check returns nil when userID may authenticate, ErrAccountSuspended when
// it must not (suspended, deleted, or malformed id — fail-closed), and any
// other error for transient lookup failures (caller maps those to 503).
func (g *AccountGuard) Check(ctx context.Context, userID string) error {
	if g == nil || g.Queries == nil {
		return nil
	}
	if status, ok := g.Cache.Get(ctx, userID); ok {
		return UserMayAuthenticate(status)
	}
	uid, err := util.ParseUUID(userID)
	if err != nil {
		return ErrAccountSuspended
	}
	status, err := g.Queries.GetUserAccountStatus(ctx, uid)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrAccountSuspended
		}
		return err
	}
	g.Cache.Set(ctx, userID, status)
	return UserMayAuthenticate(status)
}
```
(Imports: `context`, `errors`, `github.com/jackc/pgx/v5`, `github.com/jackc/pgx/v5/pgtype`, `github.com/multica-ai/multica/server/internal/util`. If `internal/util` importing from `internal/auth` creates a cycle, inline `pgtype.UUID.Scan`-based parsing instead — check `util.ParseUUID`'s package first.)

- [ ] **Step 4: Run test**

Run: `(cd server && go test ./internal/auth -run TestAccountGuardCheck -v)`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add server/internal/auth/account.go server/internal/auth/account_cache.go server/internal/auth/account_guard_test.go
git commit -m "feat(auth): AccountGuard with cached account_status lookups"
```

---

### Task 4: enforce in HTTP middlewares (Auth + DaemonAuth)

**Files:**
- Modify: `server/internal/middleware/auth.go` (replace `rejectTemporarilyDisabledUser`, lines 20-32; call sites at 100, 157, 186, 206, 256; signature at 51)
- Modify: `server/internal/middleware/daemon_auth.go` (call sites at 173, 197, 218, 262; `DaemonAuth` signature)
- Modify: `server/cmd/server/router.go` (construct one shared `*auth.AccountGuard` next to where `PATCache` is built; pass to both middlewares)
- Test: `server/internal/middleware/auth_suspension_test.go`

**Interfaces:**
- Consumes: `auth.AccountGuard.Check`, `auth.AccountSuspendedMessage/Code`, `auth.ErrAccountSuspended`.
- Produces: `middleware.Auth(queries, patCache, cloudPAT, guard *auth.AccountGuard)`, `middleware.DaemonAuth(... , guard *auth.AccountGuard)` (append guard as the last param of the existing signature), shared helper `rejectSuspendedUser(w, r, guard, userID, authPath) bool` in `middleware/auth.go` used by BOTH middlewares (they share the package).

- [ ] **Step 1: Write the failing test**

Follow the package's existing middleware test setup (grep `func TestAuth` in `server/internal/middleware/` and mirror its harness — real DB via the shared test helpers). Core cases:

```go
// Sketch — adapt to the package's existing test harness:
func TestAuthRejectsSuspendedJWT(t *testing.T) {
	// 1. create user via test queries, set account_status='suspended'
	// 2. mint a JWT for that user (reuse the helper the existing auth tests use)
	// 3. run a request through middleware.Auth(queries, nil, nil, guard)
	// 4. expect 403, body containing "ACCOUNT_SUSPENDED"
}

func TestAuthRejectsSuspendedPATCacheHit(t *testing.T) {
	// Seed patCache with the token hash (simulating a warm cache), suspend
	// the user, expect 403 — proves the cache-hit path checks suspension.
	// Use a nil-redis AccountGuard cache so the status read hits the DB.
}

func TestAuthAllowsActiveUser(t *testing.T) { /* 200 passthrough */ }
```

- [ ] **Step 2: Run to verify failure**

Run: `(cd server && go test ./internal/middleware -run 'Suspended|SuspendedPAT' -v)`
Expected: FAIL (compile error: Auth signature / undefined rejectSuspendedUser)

- [ ] **Step 3: Implement**

Replace `rejectTemporarilyDisabledUser` in `middleware/auth.go` with:

```go
// rejectSuspendedUser enforces account_status on every authenticated path,
// including cache-hit branches — a suspended user's next request fails even
// while the PAT cache is warm. Shared by Auth and DaemonAuth.
func rejectSuspendedUser(w http.ResponseWriter, r *http.Request, guard *auth.AccountGuard, userID, authPath string) bool {
	err := guard.Check(r.Context(), userID)
	if err == nil {
		return false
	}
	if errors.Is(err, auth.ErrAccountSuspended) {
		slog.Warn("auth: suspended user rejected",
			"path", r.URL.Path, "user_id", userID, "auth_path", authPath)
		http.Error(w,
			`{"error":"`+auth.AccountSuspendedMessage+`","code":"`+auth.AccountSuspendedCode+`"}`,
			http.StatusForbidden)
		return true
	}
	slog.Error("auth: account status lookup failed", "path", r.URL.Path, "error", err)
	http.Error(w, `{"error":"account status unavailable"}`, http.StatusServiceUnavailable)
	return true
}
```
(Keep the Content-Type behavior consistent with the file's existing `writeError` — if the package has `writeError`, prefer it and add a code-aware variant beside it.)

Then mechanically at each of the 9 call sites replace
`rejectTemporarilyDisabledUser(w, r, userID, email, "jwt")` → `rejectSuspendedUser(w, r, guard, userID, "jwt")` (drop the email argument everywhere — the check is now by ID only), thread `guard *auth.AccountGuard` through `Auth(...)` and `DaemonAuth(...)` params, and update the two construction sites in `router.go`:

```go
accountGuard := &auth.AccountGuard{Queries: queries, Cache: auth.NewAccountStatusCache(rdb)}
```
(`rdb` = the same Redis client the PATCache uses; grep `NewPATCache(` in router.go.)

Do NOT delete `temporary_disabled_users.go` yet — `handler/auth.go` and `hub.go` still reference it until Tasks 5–6.

- [ ] **Step 4: Run tests**

Run: `(cd server && go test ./internal/middleware -v -run 'Auth')`
Expected: new tests PASS, existing auth middleware tests PASS.

- [ ] **Step 5: Commit**

```bash
git add server/internal/middleware/ server/cmd/server/router.go
git commit -m "feat(server): enforce account_status in Auth and DaemonAuth middlewares"
```

---

### Task 5: enforce on login paths, delete the emergency denylist

**Files:**
- Modify: `server/internal/handler/auth.go` (lines 152-164 `issueJWT`, 170-204 `findOrCreateUser`, 288-292 `SendCode`, 314-318 `VerifyCode` existing-user check, 380-384, 578-590 GoogleLogin, and the `errors.Is(err, auth.ErrTemporarilyDisabledUser)` branches at 407, 425, 587, 633, 676)
- Delete: `server/internal/auth/temporary_disabled_users.go`
- Test: `server/internal/handler/auth_suspension_test.go`

**Interfaces:**
- Consumes: `db.User.AccountStatus`, `auth.UserMayAuthenticate`, `auth.ErrAccountSuspended`, `writeErrorCode` (`handler/handler.go:496`).
- Produces: every login/token-mint path returns `403` body `{"error":"account suspended","code":"ACCOUNT_SUSPENDED"}` for suspended users.

- [ ] **Step 1: Write the failing test**

Mirror the existing handler test harness (`testHandler` fixtures used across `server/internal/handler/*_test.go`):

```go
func TestVerifyCodeRejectsSuspendedUser(t *testing.T) {
	// create user, SetUserAccountStatus(suspended), drive SendCode+VerifyCode
	// (or call findOrCreateUser directly), expect 403 + code ACCOUNT_SUSPENDED
}

func TestIssueCliTokenRejectsSuspendedUser(t *testing.T) { /* 403 + code */ }
```

- [ ] **Step 2: Run to verify failure**

Run: `(cd server && go test ./internal/handler -run Suspend -v)`
Expected: FAIL

- [ ] **Step 3: Implement**

- `issueJWT`: replace the denylist check with
  ```go
  if err := auth.UserMayAuthenticate(user.AccountStatus); err != nil {
      return "", err
  }
  ```
- `findOrCreateUser`: DELETE the email-only pre-check (lines 171-173 — the row lookup right below is now authoritative); replace the post-lookup check (180-182) with `auth.UserMayAuthenticate(user.AccountStatus)`.
- `SendCode` (290), `VerifyCode` (382), `GoogleLogin` (580): DELETE the email-only pre-checks; the `findOrCreateUser` / existing-user checks cover them. `VerifyCode`'s existing-user check (316) becomes `auth.UserMayAuthenticate(existingUser.AccountStatus)`.
- Every `errors.Is(err, auth.ErrTemporarilyDisabledUser)` branch becomes `errors.Is(err, auth.ErrAccountSuspended)` writing
  `writeErrorCode(w, http.StatusForbidden, auth.AccountSuspendedCode, auth.AccountSuspendedMessage)`.
- Delete `server/internal/auth/temporary_disabled_users.go` and its test if one exists. `grep -rn "TemporarilyDisabled" server/` must return zero hits (hub.go is handled in Task 6 — if doing tasks in order, leave hub.go compiling by converting its two call sites in this task to the checker parameter added in Task 6, or reorder: do Task 6's hub changes in the same commit if the build cannot be split cleanly. Prefer one commit covering both if compilation forces it.)

- [ ] **Step 4: Run tests**

Run: `(cd server && go test ./internal/handler -run 'Suspend|Auth' -v && go vet ./...)`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add server/internal/handler/auth.go server/internal/auth/
git commit -m "feat(server): enforce account_status on login and token-mint paths"
```

---

### Task 6: WebSocket enforcement + Hub.DisconnectUser

**Files:**
- Modify: `server/internal/realtime/hub.go` (`authenticateToken` at 679, its two denylist checks at 688/714; `HandleWebSocket` at 775; add `DisconnectUser`)
- Modify: `server/cmd/server/router.go` (pass the guard into `HandleWebSocket`)
- Test: `server/internal/realtime/hub_suspension_test.go`

**Interfaces:**
- Consumes: `auth.AccountGuard`.
- Produces:
  - `realtime.AccountChecker interface { Check(ctx context.Context, userID string) error }` (satisfied by `*auth.AccountGuard`).
  - `authenticateToken(tokenStr string, pr PATResolver, ac AccountChecker, ctx context.Context) (string, string)` — after resolving uid, `ac.Check` failure returns errMsg `{"error":"account suspended","code":"ACCOUNT_SUSPENDED"}`.
  - `HandleWebSocket(hub, mc, pr, ac AccountChecker, resolveSlug, w, r)` — cookie path maps that errMsg to HTTP 403 (extend the existing `account disabled` special-case at hub.go:797, matching on the new message).
  - `(h *Hub) DisconnectUser(userID string)` — best-effort `auth_error` frame then eviction of every connection whose client belongs to userID.

- [ ] **Step 1: Write the failing test**

Follow the file's existing hub tests (grep `NewHub(` in `server/internal/realtime/*_test.go`) — unit-level, no real sockets needed for DisconnectUser:

```go
func TestDisconnectUserEvictsAllUserConnections(t *testing.T) {
	// register two fake clients for user A (different workspaces) and one
	// for user B via the hub's register path used by existing tests;
	// DisconnectUser("A"); assert A's send channels are closed / clients
	// removed from h.clients, B untouched.
}

func TestAuthenticateTokenRejectsSuspended(t *testing.T) {
	// fake AccountChecker returning ErrAccountSuspended; valid JWT;
	// expect errMsg containing ACCOUNT_SUSPENDED.
}
```

- [ ] **Step 2: Run to verify failure**

Run: `(cd server && go test ./internal/realtime -run 'DisconnectUser|AuthenticateToken' -v)`
Expected: FAIL

- [ ] **Step 3: Implement**

`DisconnectUser` (place near `evictSlow`, reusing its teardown):

```go
// DisconnectUser force-closes every connection belonging to userID. Called
// when an account is suspended so an open tab loses realtime access at the
// same moment its HTTP credentials die. Best-effort: the auth_error frame
// is dropped if the send buffer is full; eviction still proceeds.
func (h *Hub) DisconnectUser(userID string) {
	payload := []byte(`{"type":"auth_error","payload":{"error":"account suspended","code":"ACCOUNT_SUSPENDED"}}`)
	h.mu.RLock()
	var targets []*Client
	for c := range h.clients {
		if c.userID == userID {
			select {
			case c.send <- payload:
			default:
			}
			targets = append(targets, c)
		}
	}
	h.mu.RUnlock()
	if len(targets) > 0 {
		h.evictSlow(targets)
	}
}
```
(Verify the `Client` struct's user field name — grep `userID` in hub.go's Client definition; if clients register into `rooms[sk(ScopeUser, uid)]`, iterating that room under RLock is equally fine.)

`authenticateToken`: add the `ac AccountChecker` param; after each uid resolution (PAT branch and JWT branch), replace the `IsTemporarilyDisabledUser*` checks with:
```go
if err := ac.Check(ctx, uid); err != nil {
    return "", `{"error":"account suspended","code":"ACCOUNT_SUSPENDED"}`
}
```
Update `HandleWebSocket`'s cookie-path 403 mapping (hub.go:797) to match the new message, and both `authenticateToken` call sites (794, 830). Update router.go's `HandleWebSocket` call to pass `accountGuard`.

- [ ] **Step 4: Run tests**

Run: `(cd server && go test ./internal/realtime -v)`
Expected: PASS (including existing hub tests)

- [ ] **Step 5: Commit**

```bash
git add server/internal/realtime/ server/cmd/server/router.go
git commit -m "feat(realtime): account_status on WS auth and DisconnectUser eviction"
```

---

### Task 7: ADMIN_EMAILS config, requireSystemAdmin, GET /api/admin/users, GetMe flag

**Files:**
- Modify: `server/internal/handler/handler.go` (Config struct at :63 — add `AdminEmails []string`)
- Modify: wherever `handler.Config` is populated (grep `AllowedEmails:` under `server/cmd/` — add `AdminEmails` from `ADMIN_EMAILS`, comma-separated, trimmed, lowercased)
- Create: `server/internal/handler/admin.go`
- Modify: `server/internal/handler/auth.go` (`UserResponse` + `GetMe` at 453)
- Modify: `server/cmd/server/router.go` (new authed route group)
- Test: `server/internal/handler/admin_test.go`

**Interfaces:**
- Produces:
  - `(h *Handler) isSystemAdmin(email string) bool` — case-insensitive membership in `h.cfg.AdminEmails`; false when the list is empty (feature off by default).
  - `(h *Handler) requireSystemAdmin(w, r) (adminUser db.User, ok bool)` — loads the user by `X-User-ID` header (`requireUserID` helper), 403 `{"error":"forbidden"}` when not an admin. Never trust `X-User-Email` (only the JWT path sets it).
  - `(h *Handler) ListAllUsers(w, r)` → `{"users":[AdminUserResponse...]}` where `AdminUserResponse{ID, Name, Email, AvatarURL *string, AccountStatus, CreatedAt string}` (json tags snake_case: `account_status`, `created_at`, `avatar_url`).
  - `UserResponse.IsSystemAdmin bool \`json:"is_system_admin,omitempty"\`` — set ONLY in `GetMe` (after `h.userToResponse`), not in `userToResponse` itself.
  - Routes (inside the authed group that already applies `middleware.Auth` — find where `/api/me` is mounted in router.go and add beside it):
    `r.Get("/api/admin/users", h.ListAllUsers)`; the PATCH route lands in Task 8.

- [ ] **Step 1: Write the failing test**

```go
func TestListAllUsersRequiresSystemAdmin(t *testing.T) {
	// non-admin authed request -> 403; ADMIN_EMAILS-listed user -> 200 with
	// users array containing account_status fields.
}

func TestGetMeReportsSystemAdmin(t *testing.T) {
	// admin -> is_system_admin true; non-admin -> field absent/false.
}
```
(Config injection: the handler tests construct `Handler` with a `Config` — set `AdminEmails: []string{adminUser.Email}` directly rather than via env.)

- [ ] **Step 2: Run to verify failure**

Run: `(cd server && go test ./internal/handler -run 'SystemAdmin|ListAllUsers' -v)`
Expected: FAIL

- [ ] **Step 3: Implement** (`admin.go`)

```go
package handler

import (
	"net/http"
	"strings"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// isSystemAdmin reports whether email belongs to the ADMIN_EMAILS allowlist.
// An empty allowlist means the deployment has no system admins — the admin
// API is entirely inert by default.
func (h *Handler) isSystemAdmin(email string) bool {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return false
	}
	for _, a := range h.cfg.AdminEmails {
		if strings.ToLower(strings.TrimSpace(a)) == email {
			return true
		}
	}
	return false
}

// requireSystemAdmin resolves the authenticated user and gates on the
// allowlist. Identity comes from the user row (by X-User-ID), never from
// X-User-Email — only the JWT auth path sets the email header.
func (h *Handler) requireSystemAdmin(w http.ResponseWriter, r *http.Request) (db.User, bool) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return db.User{}, false
	}
	user, err := h.Queries.GetUser(r.Context(), parseUUID(userID))
	if err != nil {
		writeError(w, http.StatusForbidden, "forbidden")
		return db.User{}, false
	}
	if !h.isSystemAdmin(user.Email) {
		writeError(w, http.StatusForbidden, "forbidden")
		return db.User{}, false
	}
	return user, true
}

type AdminUserResponse struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	Email         string  `json:"email"`
	AvatarURL     *string `json:"avatar_url"`
	AccountStatus string  `json:"account_status"`
	CreatedAt     string  `json:"created_at"`
}

func (h *Handler) ListAllUsers(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireSystemAdmin(w, r); !ok {
		return
	}
	users, err := h.Queries.ListUsersWithStatus(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list users")
		return
	}
	resp := make([]AdminUserResponse, 0, len(users))
	for _, u := range users {
		resp = append(resp, AdminUserResponse{
			ID:            uuidToString(u.ID),
			Name:          u.Name,
			Email:         u.Email,
			AvatarURL:     h.resolveAvatarURLPtr(textToPtr(u.AvatarUrl)),
			AccountStatus: u.AccountStatus,
			CreatedAt:     timestampToString(u.CreatedAt),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": resp})
}
```

`GetMe`: after building the response, add `resp := h.userToResponse(user); resp.IsSystemAdmin = h.isSystemAdmin(user.Email); writeJSON(w, http.StatusOK, resp)`.

Config population (in the cmd/server file that builds `handler.Config`):
```go
AdminEmails: splitAndTrimCSV(os.Getenv("ADMIN_EMAILS")),
```
(reuse however `AllowedEmails` is parsed there — same helper, same style.)

- [ ] **Step 4: Run tests**

Run: `(cd server && go test ./internal/handler -run 'SystemAdmin|ListAllUsers|GetMe' -v)`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add server/internal/handler/ server/cmd/server/
git commit -m "feat(server): system-admin allowlist and global user listing"
```

---

### Task 8: PATCH /api/admin/users/{id}/status + suspension convergence

**Files:**
- Modify: `server/internal/handler/admin.go`
- Modify: `server/internal/handler/handler.go` (Handler struct: add `AccountGuard *auth.AccountGuard` and `DisconnectUser func(userID string)` fields)
- Modify: `server/cmd/server/router.go` (wire both fields + the route)
- Test: `server/internal/handler/admin_test.go` (extend)

**Interfaces:**
- Consumes: Task 1 queries (`SetUserAccountStatus`, `ListAgentRuntimesByOwnerAllWorkspaces`, existing `CancelAgentTasksByRuntimeOrAgent`, `ForceOfflineRuntimesByIDs`), `auth.AccountGuard.Cache.Invalidate`, `hub.DisconnectUser` (via the injected func).
- Produces: `PATCH /api/admin/users/{id}/status`, body `{"status":"active"|"suspended"}`, 200 → updated `AdminUserResponse`. 400 on bad UUID or unknown status; 403 for non-admins and for self-suspension (`{"error":"cannot change your own account status"}`).

- [ ] **Step 1: Write the failing tests**

```go
func TestSetAccountStatusSuspendsAndRestores(t *testing.T) {
	// admin PATCHes target to suspended -> 200, DB row shows 'suspended';
	// PATCH back to active -> 200, row 'active'.
}

func TestSetAccountStatusRejectsSelf(t *testing.T) { /* 403 */ }

func TestSetAccountStatusRejectsUnknownStatus(t *testing.T) { /* 400 for "banned" */ }

func TestSuspendCancelsRuntimesAndTasks(t *testing.T) {
	// seed a runtime owned by target + a queued task on it; suspend;
	// assert task status cancelled and runtime forced offline.
}
```

- [ ] **Step 2: Run to verify failure**

Run: `(cd server && go test ./internal/handler -run SetAccountStatus -v)`
Expected: FAIL

- [ ] **Step 3: Implement** (append to `admin.go`)

```go
type SetAccountStatusRequest struct {
	Status string `json:"status"`
}

func (h *Handler) SetUserAccountStatus(w http.ResponseWriter, r *http.Request) {
	admin, ok := h.requireSystemAdmin(w, r)
	if !ok {
		return
	}
	targetID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	var req SetAccountStatusRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Status != auth.AccountStatusActive && req.Status != auth.AccountStatusSuspended {
		writeError(w, http.StatusBadRequest, "invalid status")
		return
	}
	if uuidToString(targetID) == uuidToString(admin.ID) {
		writeError(w, http.StatusForbidden, "cannot change your own account status")
		return
	}

	updated, err := h.Queries.SetUserAccountStatus(r.Context(), db.SetUserAccountStatusParams{
		ID:            targetID,
		AccountStatus: req.Status,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}

	targetIDStr := uuidToString(targetID)
	// Revocation must beat the cache TTL: without this the "next request
	// fails" promise silently becomes "fails within 10 minutes".
	if h.AccountGuard != nil {
		h.AccountGuard.Cache.Invalidate(r.Context(), targetIDStr)
	}

	if req.Status == auth.AccountStatusSuspended {
		h.quiesceSuspendedUser(r.Context(), targetID, targetIDStr)
	}

	writeJSON(w, http.StatusOK, AdminUserResponse{
		ID:            targetIDStr,
		Name:          updated.Name,
		Email:         updated.Email,
		AvatarURL:     h.resolveAvatarURLPtr(textToPtr(updated.AvatarUrl)),
		AccountStatus: updated.AccountStatus,
		CreatedAt:     timestampToString(updated.CreatedAt),
	})
}

// quiesceSuspendedUser converges runtime-side state after a suspension:
// in-flight tasks on the user's runtimes are cancelled (so agents stop
// gracefully) and the runtimes are forced offline. Unlike member removal
// (revokeAndRemoveMember) nothing is archived or deleted — suspension is
// reversible. Failures are logged, not surfaced: the status flip and cache
// invalidation above are the security boundary; this is cleanup.
func (h *Handler) quiesceSuspendedUser(ctx context.Context, userID pgtype.UUID, userIDStr string) {
	runtimes, err := h.Queries.ListAgentRuntimesByOwnerAllWorkspaces(ctx, userID)
	if err != nil {
		slog.Error("suspend: list runtimes failed", "user_id", userIDStr, "error", err)
	} else if len(runtimes) > 0 {
		runtimeIDs := make([]pgtype.UUID, len(runtimes))
		for i, rt := range runtimes {
			runtimeIDs[i] = rt.ID
		}
		cancelled, err := h.Queries.CancelAgentTasksByRuntimeOrAgent(ctx, db.CancelAgentTasksByRuntimeOrAgentParams{
			RuntimeIds: runtimeIDs,
			AgentIds:   nil,
		})
		if err != nil {
			slog.Error("suspend: cancel tasks failed", "user_id", userIDStr, "error", err)
		} else if h.TaskService != nil {
			// Group per workspace: BroadcastCancelledTasks takes one workspace.
			byWs := map[string][]db.AgentTaskQueue{}
			for _, t := range cancelled {
				byWs[uuidToString(t.WorkspaceID)] = append(byWs[uuidToString(t.WorkspaceID)], t)
			}
			for wsID, tasks := range byWs {
				h.TaskService.BroadcastCancelledTasks(ctx, wsID, tasks)
			}
		}
		if _, err := h.Queries.ForceOfflineRuntimesByIDs(ctx, runtimeIDs); err != nil {
			slog.Error("suspend: force offline failed", "user_id", userIDStr, "error", err)
		}
	}

	if h.DisconnectUser != nil {
		h.DisconnectUser(userIDStr)
	}
}
```
(Verify `AgentTaskQueue.WorkspaceID` field name against the generated model; adapt. Verify `parseUUIDOrBadRequest`'s exact signature — grep it in `handler/` — the plan sketch assumes `(w, s, fieldName)` returning `(pgtype.UUID, bool)`.)

Router: `r.Patch("/api/admin/users/{id}/status", h.SetUserAccountStatus)` beside the Task 7 route; wire `h.AccountGuard = accountGuard` and `h.DisconnectUser = hub.DisconnectUser` where the Handler is constructed.

- [ ] **Step 4: Run tests**

Run: `(cd server && go test ./internal/handler -run 'SetAccountStatus|Suspend' -v && go vet ./...)`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add server/internal/handler/ server/cmd/server/
git commit -m "feat(server): admin account-status endpoint with suspension convergence"
```

---

### Task 9: core API — schemas, client methods, session-kill on ACCOUNT_SUSPENDED

**Files:**
- Modify: `packages/core/api/client.ts` (fetchRaw at ~646; options type; new admin methods near getMe at :723)
- Modify: `packages/core/types` user type (add `is_system_admin?: boolean`; find the `User` type file via `grep -rn "onboarded_at" packages/core/types`)
- Create: `packages/core/admin/schema.ts` (zod schemas), `packages/core/admin/queries.ts`
- Modify: `packages/core/platform/core-provider.tsx` (:59 — wire the new callback, fix onUnauthorized)
- Test: `packages/core/admin/schema.test.ts` (node env), `packages/core/api/client-suspended.test.ts` (node env)

**Interfaces:**
- Produces:
  - `ACCOUNT_SUSPENDED_CODE = "ACCOUNT_SUSPENDED"` and `SESSION_ENDED_REASON_KEY = "multica_session_ended_reason"` exported from `packages/core/auth/utils.ts` (or a new small `packages/core/auth/session-ended.ts` — keep it importable by both client.ts and the login page without cycles).
  - `AdminUser` type `{ id: string; name: string; email: string; avatar_url: string | null; account_status: "active" | "suspended" | "unknown"; created_at: string }` — schema maps unrecognized statuses to `"unknown"` (server-driven enum, default branch).
  - `api.getAdminUsers(): Promise<AdminUser[]>` (GET `/api/admin/users`, `parseWithFallback(adminUserListSchema, json).users ?? []`).
  - `api.setUserAccountStatus(userId: string, status: "active" | "suspended"): Promise<AdminUser>` (PATCH).
  - `adminKeys.users()` + `adminUsersOptions()` query options (admin data is NOT workspace-scoped — no wsId in the key; key: `["admin","users"]`).
  - `ApiClientOptions.onSessionRejected?: (reason: "unauthorized" | "account_suspended") => void` replacing-wrapping the current `onUnauthorized` usage internally (keep `onUnauthorized` working for back-compat; call both).
  - fetchRaw behavior: parse the error body FIRST, then: 401 → `handleUnauthorized()` (as today) AND `onSessionRejected?.("unauthorized")`; 403 with `body.code === "ACCOUNT_SUSPENDED"` → clear token + `onSessionRejected?.("account_suspended")`.
  - core-provider wiring:
    ```ts
    onSessionRejected: (reason) => {
      storage.removeItem("multica_token");
      if (reason === "account_suspended") {
        storage.setItem(SESSION_ENDED_REASON_KEY, "account_suspended");
      }
      // authStore is assigned later in this initializer; the callback only
      // fires on subsequent requests, after assignment.
      authStore?.getState().logout();
    },
    ```
    (`logout()` already clears token/workspace/user → route guards redirect to /login. Verify `authStore` is the module-scope variable in core-provider; it is assigned ~20 lines below the ApiClient construction.)

- [ ] **Step 1: Write failing tests**

`packages/core/admin/schema.test.ts` (`// @vitest-environment node`): valid payload parses; **malformed payload** (missing `users`, wrong types, `account_status: "banned"`) degrades to `[]` / `"unknown"` instead of throwing.

`packages/core/api/client-suspended.test.ts` (`// @vitest-environment node`): stub global `fetch` returning 403 + `{"error":"account suspended","code":"ACCOUNT_SUSPENDED"}`; assert `onSessionRejected` fires with `"account_suspended"` and the thrown `ApiError.status === 403`; a plain 403 (no code) must NOT fire it.

- [ ] **Step 2: Run to verify failure**

Run: `pnpm --filter @multica/core test -- admin`
Expected: FAIL

- [ ] **Step 3: Implement** (schemas sketch)

```ts
// packages/core/admin/schema.ts
import { z } from "zod";

export const adminAccountStatusSchema = z
  .string()
  .transform((s) => (s === "active" || s === "suspended" ? s : ("unknown" as const)));

export const adminUserSchema = z.object({
  id: z.string(),
  name: z.string().default(""),
  email: z.string().default(""),
  avatar_url: z.string().nullish().transform((v) => v ?? null),
  account_status: adminAccountStatusSchema.default("unknown"),
  created_at: z.string().default(""),
});

export const adminUserListSchema = z.object({
  users: z.array(adminUserSchema).default([]),
});

export type AdminUser = z.infer<typeof adminUserSchema>;
```
Follow the repo's existing schema/`parseWithFallback` idioms (`packages/core/api/schema.ts`) — match how other endpoint schemas are registered and logged.

- [ ] **Step 4: Run tests**

Run: `pnpm --filter @multica/core test`
Expected: PASS (full package — client.ts changed).

- [ ] **Step 5: Commit**

```bash
git add packages/core/
git commit -m "feat(core): admin users API and ACCOUNT_SUSPENDED session kill"
```

---

### Task 10: ws-client — stop reconnecting on auth_error

**Files:**
- Modify: `packages/core/api/ws-client.ts` (onmessage at ~131, scheduleReconnect at ~160, options/ctor)
- Test: `packages/core/api/ws-client-auth-error.test.ts` (node env, fake WebSocket — follow the file's existing test if one exists, else mock `WebSocket` global)

**Interfaces:**
- Produces: constructor option `onAuthRejected?: () => void`. New private flag `authRejected = false`. In `onmessage`, before the generic dispatch:
  ```ts
  if ((msg as any).type === "auth_error") {
    const code = (msg as { payload?: { code?: string } }).payload?.code;
    this.logger.warn("ws: auth rejected, stopping reconnects", { code });
    this.authRejected = true;
    this.ws?.close();
    if (code === "ACCOUNT_SUSPENDED") this.options.onAuthRejected?.();
    return;
  }
  ```
  `onclose` and `scheduleReconnect` early-return when `this.authRejected`; an explicit `connect()` call resets the flag (so a fresh login can reconnect).
- Wiring: wherever the app constructs the ws client (grep `new WSClient(`/`ws-client` consumers in `packages/core`), pass `onAuthRejected` through to the same session-kill used in Task 9 (`onSessionRejected("account_suspended")` path — thread via provider options like the api client's callback).

- [ ] **Step 1: Write the failing test** — feed an `auth_error` frame with code ACCOUNT_SUSPENDED; assert no reconnect timer is scheduled (spy on `setTimeout` or expose attempt count) and `onAuthRejected` fired; feed a normal close → reconnect still scheduled when flag unset.

- [ ] **Step 2: Run to verify failure**

Run: `pnpm --filter @multica/core test -- ws-client`
Expected: FAIL

- [ ] **Step 3: Implement** as specified above.

- [ ] **Step 4: Run tests**

Run: `pnpm --filter @multica/core test`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add packages/core/
git commit -m "feat(core): stop WS reconnect loop on auth_error rejection"
```

---

### Task 11: login page suspended notice + i18n

**Files:**
- Modify: `packages/views/auth/login-page.tsx`
- Modify: locale files `packages/views/locales/{en,zh-Hans,ja,ko}/<auth namespace file>` (find the namespace login-page.tsx uses via its `useT("...")` call; `parity.test.ts` enforces key parity across all four locales — add every key to all four)
- Test: `packages/views/auth/login-page.test.tsx` (extend)

**Interfaces:**
- Consumes: `SESSION_ENDED_REASON_KEY` from Task 9; `ApiError` (`.body.code`).
- Produces two behaviors:
  1. On mount, if `storage.getItem(SESSION_ENDED_REASON_KEY) === "account_suspended"` → render a dismissible notice `t($ => $.login.account_suspended_notice)` and REMOVE the key (show-once).
  2. `sendCode`/`verifyCode`/Google error handling: when the caught error is `ApiError` with `body?.code === "ACCOUNT_SUSPENDED"` → show the same suspended message inline instead of the generic failure copy.
- Copy (zh-Hans): `账号已被禁用，请联系管理员。` en: `This account has been suspended. Contact your administrator.` (ja/ko: translate equivalently; follow the file's existing tone.)

- [ ] **Step 1: Write the failing test** — mock storage with the reason key set → notice renders and key is cleared; mock `sendCode` rejecting with an ApiError carrying the code → inline suspended message (assert via the i18n key's English text, matching how existing login-page tests assert copy).

- [ ] **Step 2: Run to verify failure**

Run: `pnpm --filter @multica/views test -- login-page`
Expected: FAIL

- [ ] **Step 3: Implement.** Storage access in views goes through whatever adapter login-page already uses (check its imports — do not touch `localStorage` directly if an adapter is in scope; if the page has no storage access today, import the `StorageAdapter` the same way `core-provider` consumers do or read via a small helper exported next to `SESSION_ENDED_REASON_KEY`).

- [ ] **Step 4: Run tests**

Run: `pnpm --filter @multica/views test -- login-page && pnpm --filter @multica/views test -- parity`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add packages/views/ packages/core/
git commit -m "feat(views): suspended-account notice on login page"
```

---

### Task 12: /admin page + sidebar entry + web/desktop wiring

**Files:**
- Modify: `packages/core/paths/paths.ts` (add `admin: () => "/admin"` beside `login` at :67; check `packages/core/paths/reserved-slugs.ts` — if `admin` is not reserved, add it to `server/internal/handler/reserved_slugs.json`, run `pnpm generate:reserved-slugs`, commit the generated file)
- Create: `packages/views/admin/admin-users-page.tsx`, `packages/views/admin/index.ts`
- Modify: `packages/views/layout/app-sidebar.tsx` (menu item before the logout group at :717-723)
- Create: `apps/web/app/admin/page.tsx`
- Modify: `apps/desktop/src/renderer/src/routes.tsx` (+ page file under `apps/desktop/src/renderer/src/pages/admin-page.tsx`)
- Modify: locale files (all four) — new `admin` keys (or extend the settings namespace; prefer a new `admin` namespace file mirroring how `settings` is organized)
- Test: `packages/views/admin/admin-users-page.test.tsx`

**Interfaces:**
- Consumes: `adminUsersOptions()`, `api.setUserAccountStatus`, `useAuthStore` user (`is_system_admin === true`), `useNavigation().push`, `paths.admin()`, UI kit: `Button`, `Badge`, `Card`, `AlertDialog`, `DropdownMenu`, `Input`, `ActorAvatar` — same imports as `members-tab.tsx`.
- Produces: `AdminUsersPage` — implements the approved design (design canvas "Multica 系统管理设计稿"):
  - Header: title 系统管理 (`text-title-lg font-semibold tracking-tight`), description; section header `用户 · {count}` + search input filtering by name/email (local state).
  - `SettingsCard`-style list (reuse `SettingsCard` from `packages/views/settings/components/settings-layout.tsx`): rows `flex items-center gap-3 px-4 py-3`, `ActorAvatar` lg, name `text-body font-medium` + email `text-caption text-muted-foreground`.
  - Active rows: no status badge. Suspended rows: name in `text-muted-foreground`, avatar at reduced opacity, `<Badge variant="destructive">已禁用</Badge>` with Ban icon.
  - Self row: `<Badge variant="outline">你</Badge>`, no ⋯ menu.
  - Row ⋯ menu (ghost icon-sm button): active user → destructive item 禁用账号 (Ban icon); suspended user → item 恢复账号 (RotateCcw icon).
  - Both actions confirm via `AlertDialog` (copy the `confirmAction` state pattern from `members-tab.tsx:323-328` and its dialog at :640-659). Suspend description: `禁用后，{name} 将无法登录 Multica，已登录的会话会在下一次请求时失效，进行中的任务将被取消。你可以随时恢复该账号。`
  - Mutations invalidate `adminKeys.users()`; toasts via `sonner` like members-tab.
  - All copy through `useT("admin")`; English/ja/ko translations in the same commit.
- Sidebar entry (app-sidebar.tsx, in a group before the logout separator):
  ```tsx
  {user?.is_system_admin === true && (
    <>
      <DropdownMenuGroup>
        <DropdownMenuItem onClick={() => push(paths.admin())}>
          <Shield className="h-3.5 w-3.5" />
          {t(($) => $.sidebar.system_admin)}
        </DropdownMenuItem>
      </DropdownMenuGroup>
      <DropdownMenuSeparator />
    </>
  )}
  ```
  (`Shield` is already imported in members-tab; add to app-sidebar's lucide imports. Key `sidebar.system_admin` = 系统管理 / System administration / ja / ko.)
- Web wiring: `apps/web/app/admin/page.tsx` — client component; follow the guard pattern of an existing global authed page (read `apps/web/app/(auth)/invitations/page.tsx` first and mirror its auth guard/layout); render `<AdminUsersPage />`; additionally `router.replace(paths.login())` when unauthenticated and render nothing when `user.is_system_admin !== true` (redirect to `/` — server still enforces).
- Desktop wiring: top-level route `{ path: "admin", element: <AdminPage /> }` in routes.tsx OUTSIDE the `:workspaceSlug` tree (this is a session-level full-window view, not a transition flow, so a route — not a WindowOverlay — is correct); `AdminPage` mounts `<DragStrip />` from `@multica/views/platform` as the first flex child, then `<AdminUsersPage />`; back button uses `useNavigation()`.

- [ ] **Step 1: Write the failing component test** (`admin-users-page.test.tsx`): mock `@multica/core/api` and the auth store (callable-store shape with `getState`); assert: rows render from mocked query data; suspended row shows 已禁用 badge; self row has no menu; clicking 禁用账号 opens the confirm dialog and confirming calls `api.setUserAccountStatus(id, "suspended")`.

- [ ] **Step 2: Run to verify failure**

Run: `pnpm --filter @multica/views test -- admin-users-page`
Expected: FAIL

- [ ] **Step 3: Implement** page + sidebar + web + desktop wiring as specified. No `next/*` or `react-router-dom` imports inside `packages/views/`.

- [ ] **Step 4: Run tests**

Run: `pnpm --filter @multica/views test && pnpm --filter @multica/core test && pnpm typecheck`
Expected: PASS (full views suite — a shared component changed).

- [ ] **Step 5: Commit**

```bash
git add packages/ apps/web/ apps/desktop/ server/internal/handler/reserved_slugs.json
git commit -m "feat(admin): global /admin console with account suspend/restore"
```

---

### Task 13: mobile handling + full verification

**Files:**
- Modify: `apps/mobile/data/api.ts` (401 handling at ~:280 and ~:1233 — read `apps/mobile/CLAUDE.md` FIRST per repo rules)
- Test: mobile's existing test setup if the file has one; otherwise the change is a guarded two-line addition validated by typecheck

**Interfaces:**
- Produces: mobile's unauthorized path also fires when `status === 403` and parsed body `code === "ACCOUNT_SUSPENDED"` (explicit `=== "ACCOUNT_SUSPENDED"` check), reusing the exact same logout+redirect flow `_layout.tsx:38` already implements for 401. No new UI; the login screen may show a generic message.

- [ ] **Step 1: Read `apps/mobile/CLAUDE.md`**, then implement the guarded branch at both 401 sites in `apps/mobile/data/api.ts`.

- [ ] **Step 2: Typecheck mobile**

Run: `pnpm --filter <mobile package name> typecheck` (find the package name in `apps/mobile/package.json`; fall back to `pnpm typecheck`).
Expected: PASS

- [ ] **Step 3: Full verification sweep**

```bash
pnpm typecheck
pnpm test
(cd server && go build ./... && go vet ./... && go test ./internal/auth ./internal/middleware ./internal/handler ./internal/realtime)
```
Expected: PASS. Known environmental failures (pkg/agent, internal/daemon CLI-dependent tests, pg_cron flakes) are pre-existing — verify only that suites touching this change pass; note anything skipped in the final report.

- [ ] **Step 4: Docs cross-check** — grep `server/internal/service/builtin_skills/` for auth/login/session mentions; if any SKILL.md documents login or API auth behavior affected by ACCOUNT_SUSPENDED, update it in this commit. If none, state so.

- [ ] **Step 5: Commit**

```bash
git add apps/mobile/ server/internal/service/builtin_skills/ 2>/dev/null
git commit -m "feat(mobile): handle ACCOUNT_SUSPENDED session termination"
```

---

## Self-Review Notes

- Spec coverage: migration (T1), fail-closed rule (T2), cache + PAT-cache-regression fix (T3/T4/T8 invalidation), all 5 HTTP auth paths + daemon (T4), login paths + denylist removal (T5), WS reject + DisconnectUser (T6), ADMIN_EMAILS + admin API + is_system_admin (T7/T8), suspension convergence (T8), schema/parseWithFallback/malformed tests (T9), WS client reconnect stop (T10), login notice (T11), /admin UI + entry + web/desktop wiring + reserved slug (T12), mobile (T13). CDN-signed-URL residual window: documented in spec as accepted, no task needed.
- Type consistency: `AccountGuard.Check(ctx, userID string) error` used by T4/T6; `AdminUserResponse` JSON shape matches T9's zod schema field-for-field; `SESSION_ENDED_REASON_KEY` shared by T9/T11; `onSessionRejected(reason)` shared by T9/T10 wiring.
- Line numbers cited are from the audit at plan time — treat them as anchors, re-grep before editing.
