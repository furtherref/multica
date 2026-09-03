# Runtime Cost Budget Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let workspace owners and admins cap a runtime's estimated model spend per day, week and month, both as a runtime total and per user, and refuse new agent runs at enqueue time once a limit is reached.

**Architecture:** A new `runtime_cost_budget` table holds one row per (runtime, user-or-NULL) with three nullable tick limits. The server prices spend itself from `task_usage` rows using the rate table moved out of `internal/metrics` into a new leaf package `internal/pricing`, and `TaskService.checkRuntimeCostBudget` runs in every enqueue helper right after attribution. A `GET`/`PUT /api/runtimes/{id}/budget` pair serves the runtime detail page's new collapsed `BudgetSection`, and a `budget_exceeded` dispatch reason plus a `runtime_budget_exceeded` inbox item surface refusals.

**Tech Stack:** Go 1.26, Chi, sqlc 1.31.1, pgx/v5, PostgreSQL 17; TypeScript strict, TanStack Query, zod, React, Vitest, Tailwind tokens from `packages/ui`.

**Spec:** `docs/superpowers/specs/2026/09/03/runtime-cost-budget-design.md`

## Global Constraints

- No `FOREIGN KEY` / `REFERENCES`, no cascading deletes; dependent cleanup is explicit application code (`CLAUDE.md`).
- Every index goes in its own single-statement migration using `CREATE [UNIQUE] INDEX CONCURRENTLY`; migration files come in `.up.sql`/`.down.sql` pairs; next free prefix is `452` (prefixes must stay unique after 148).
- Every up migration that builds an index concurrently must be listed in `concurrentIndexCleanups` in `server/cmd/migrate/main.go` (`TestEveryConcurrentUpBuildHasCleanup` fails otherwise).
- Period boundaries are UTC for every scope: day = UTC calendar day, week starts Monday 00:00 UTC, month starts the 1st 00:00 UTC.
- Spend the server cannot price counts as zero; browser custom prices never affect enforcement.
- Blocking happens only at enqueue time; queued and running tasks are never interrupted and the claim path is untouched.
- Money on the wire is `*_usd` numbers; storage is `BIGINT` ticks of 1e-10 USD (`pricing.CostUSDTicksPerUSD`). Limits: finite, > 0, ≤ 1,000,000 USD, at most two decimals.
- `PUT` is allowed only for workspace `owner` / `admin` via `requireWorkspaceRole`; `GET` uses `requireRuntimeReadAccess`.
- Frontend: parse every response with `parseWithFallback` + zod, never cast; server-driven enum switches need a `default` branch; locale keys must exist in `en`, `zh-Hans`, `ja`, `ko` (parity test).
- Code comments in English. Conventional commit prefixes, atomic commits. Tests beside the code they test.
- Run Go DB tests with `DATABASE_URL` pointing at a scratch database built from this branch's migrations (see the memory note on cross-branch DB pollution); `go test ./internal/handler` skips when the DB is unreachable.

---

## File Structure

**Backend (server/)**

- `migrations/452_runtime_cost_budget.{up,down}.sql` — table.
- `migrations/453_runtime_cost_budget_scope_index.{up,down}.sql` — unique concurrent index.
- `cmd/migrate/main.go` — register the concurrent index.
- `pkg/db/queries/runtime_cost_budget.sql` — budget rows, spend aggregate, notify mark.
- `internal/pricing/pricing.go` (moved from `internal/metrics/pricing.go`), `pricing_test.go` (moved), `period.go`, `period_test.go`, `cost.go`, `cost_test.go` — rate table, UTC period math, tick estimate.
- `internal/metrics/business.go` — import `pricing`.
- `internal/dispatch/reason.go` — `ReasonBudgetExceeded`.
- `internal/service/runtime_cost_budget.go`, `_test.go` — error type, status computation, enqueue check, notification.
- `internal/service/task.go` — five enqueue hooks.
- `internal/service/autopilot.go` — reason mapping.
- `internal/handler/admission.go`, `comment.go`, `issue.go`, `chat.go` — reason mapping / 409.
- `internal/handler/runtime_cost_budget.go`, `_test.go` — `GET`/`PUT`.
- `internal/handler/runtime.go`, `daemon.go`, `workspace_revoke.go` — row cleanup on runtime delete / member revoke.
- `cmd/server/router.go` — routes.

**Frontend (packages/)**

- `core/types/agent.ts` — `RuntimeCostBudget*` types.
- `core/api/schemas.ts`, `schemas.test.ts` — `RuntimeCostBudgetSchema`.
- `core/api/client.ts` — `getRuntimeCostBudget`, `updateRuntimeCostBudget`.
- `core/runtimes/queries.ts`, `queries.test.ts`, `mutations.ts` — key, options, mutation.
- `core/permissions/rules.ts`, `rules.test.ts`, `index.ts` — `canManageRuntimeBudget`.
- `core/types/inbox.ts` — `runtime_budget_exceeded`.
- `views/runtimes/budget.ts`, `budget.test.ts` — pure helpers (ticks/percent/reached count).
- `views/runtimes/components/budget-section.tsx`, `budget-section.test.tsx` — card, collapsed by default.
- `views/runtimes/components/runtime-budget-dialog.tsx`, `runtime-budget-dialog.test.tsx` — editor.
- `views/runtimes/components/runtime-detail.tsx` — mount.
- `views/issues/blocked-trigger-copy.ts`, `views/autopilots/components/run-now-toast.ts` — `budget_exceeded` copy.
- `views/inbox/components/inbox-display.ts`, `inbox-detail-label.tsx` — new inbox type.
- `views/locales/{en,zh-Hans,ja,ko}/{runtimes,issues,inbox,autopilots}.json` — copy.

---

### Task 1: Schema and sqlc queries

**Files:**
- Create: `server/migrations/452_runtime_cost_budget.up.sql`, `server/migrations/452_runtime_cost_budget.down.sql`
- Create: `server/migrations/453_runtime_cost_budget_scope_index.up.sql`, `server/migrations/453_runtime_cost_budget_scope_index.down.sql`
- Modify: `server/cmd/migrate/main.go` (the `concurrentIndexCleanups` map, last entry currently `"446_issue_properties_bigm_index"`)
- Create: `server/pkg/db/queries/runtime_cost_budget.sql`
- Generated: `server/pkg/db/generated/runtime_cost_budget.sql.go`, `models.go` via `make sqlc`

**Interfaces:**
- Produces sqlc methods on `*db.Queries`: `ListRuntimeCostBudgets(ctx, runtimeID pgtype.UUID) ([]db.RuntimeCostBudget, error)`, `UpsertRuntimeCostBudget(ctx, db.UpsertRuntimeCostBudgetParams) (db.RuntimeCostBudget, error)`, `DeleteRuntimeCostBudgetsExcept(ctx, db.DeleteRuntimeCostBudgetsExceptParams) error`, `DeleteRuntimeCostBudgetsForRuntime(ctx, runtimeID pgtype.UUID) error`, `DeleteRuntimeCostBudgetsForWorkspaceUser(ctx, db.DeleteRuntimeCostBudgetsForWorkspaceUserParams) error`, `ListRuntimeSpendSince(ctx, db.ListRuntimeSpendSinceParams) ([]db.ListRuntimeSpendSinceRow, error)`, `MarkRuntimeCostBudgetNotified(ctx, db.MarkRuntimeCostBudgetNotifiedParams) (int64, error)`.
- `db.RuntimeCostBudget` has fields `ID, WorkspaceID, RuntimeID pgtype.UUID; UserID pgtype.UUID; DailyLimitUsdTicks, WeeklyLimitUsdTicks, MonthlyLimitUsdTicks pgtype.Int8; DailyNotifiedPeriodStart, WeeklyNotifiedPeriodStart, MonthlyNotifiedPeriodStart pgtype.Timestamptz; UpdatedBy pgtype.UUID; CreatedAt, UpdatedAt pgtype.Timestamptz`.

- [ ] **Step 1: Write the table migration**

`server/migrations/452_runtime_cost_budget.up.sql`:

```sql
-- Per-runtime model cost budgets. One row per scope: user_id IS NULL is the
-- runtime total (blocks every user when reached); a non-NULL user_id caps the
-- tasks whose agent that user owns. A NULL limit means unlimited for that
-- period, and a row whose three limits are all NULL is deleted by the
-- application rather than kept, so "no row" is the only representation of
-- "no budget". Limits are ticks of 1e-10 USD (pricing.CostUSDTicksPerUSD).
-- The *_notified_period_start columns remember which period already got its
-- "limit reached" inbox notice so the notice fires once per period.
-- No foreign keys by repository rule; runtime delete and member revoke remove
-- rows in application code.
CREATE TABLE IF NOT EXISTS runtime_cost_budget (
    id                              UUID PRIMARY KEY,
    workspace_id                    UUID NOT NULL,
    runtime_id                      UUID NOT NULL,
    user_id                         UUID,
    daily_limit_usd_ticks           BIGINT,
    weekly_limit_usd_ticks          BIGINT,
    monthly_limit_usd_ticks         BIGINT,
    daily_notified_period_start     TIMESTAMPTZ,
    weekly_notified_period_start    TIMESTAMPTZ,
    monthly_notified_period_start   TIMESTAMPTZ,
    updated_by                      UUID,
    created_at                      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT runtime_cost_budget_limits_positive CHECK (
        (daily_limit_usd_ticks   IS NULL OR daily_limit_usd_ticks   > 0) AND
        (weekly_limit_usd_ticks  IS NULL OR weekly_limit_usd_ticks  > 0) AND
        (monthly_limit_usd_ticks IS NULL OR monthly_limit_usd_ticks > 0)
    )
);
```

`server/migrations/452_runtime_cost_budget.down.sql`:

```sql
DROP TABLE IF EXISTS runtime_cost_budget;
```

- [ ] **Step 2: Write the index migration (single statement)**

`server/migrations/453_runtime_cost_budget_scope_index.up.sql`:

```sql
-- One budget row per (runtime, user) scope. NULLS NOT DISTINCT makes the
-- runtime-total row (user_id IS NULL) unique too, and lets the upsert in
-- runtime_cost_budget.sql target this index with ON CONFLICT.
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_runtime_cost_budget_scope
    ON runtime_cost_budget (runtime_id, user_id) NULLS NOT DISTINCT;
```

`server/migrations/453_runtime_cost_budget_scope_index.down.sql`:

```sql
DROP INDEX CONCURRENTLY IF EXISTS idx_runtime_cost_budget_scope;
```

- [ ] **Step 3: Register the concurrent index in the migrate runner**

In `server/cmd/migrate/main.go`, inside `var concurrentIndexCleanups = map[string]string{`, add after the `"446_issue_properties_bigm_index"` line:

```go
	"453_runtime_cost_budget_scope_index":                       "idx_runtime_cost_budget_scope",
```

- [ ] **Step 4: Run the migration lint and runner tests**

Run: `cd server && go test ./internal/migrations ./cmd/migrate -count=1`
Expected: PASS (if `TestEveryConcurrentUpBuildHasCleanup` or a down-map test names a missing entry, add exactly the entry it names).

- [ ] **Step 5: Apply migrations to the scratch database**

Run: `cd server && go run ./cmd/migrate up`
Expected: log lines for 452 and 453, exit 0.

- [ ] **Step 6: Write the sqlc queries**

`server/pkg/db/queries/runtime_cost_budget.sql`:

```sql
-- name: ListRuntimeCostBudgets :many
SELECT * FROM runtime_cost_budget
WHERE runtime_id = $1
ORDER BY user_id NULLS FIRST, created_at;

-- name: UpsertRuntimeCostBudget :one
-- Replaces the three limits of one scope. A changed limit clears the notified
-- markers so a raised or lowered cap notifies again in the current period.
INSERT INTO runtime_cost_budget (
    id, workspace_id, runtime_id, user_id,
    daily_limit_usd_ticks, weekly_limit_usd_ticks, monthly_limit_usd_ticks,
    updated_by
) VALUES (
    $1, $2, $3, sqlc.narg('user_id'),
    sqlc.narg('daily_limit_usd_ticks'), sqlc.narg('weekly_limit_usd_ticks'), sqlc.narg('monthly_limit_usd_ticks'),
    $4
)
ON CONFLICT (runtime_id, user_id) DO UPDATE SET
    daily_limit_usd_ticks   = EXCLUDED.daily_limit_usd_ticks,
    weekly_limit_usd_ticks  = EXCLUDED.weekly_limit_usd_ticks,
    monthly_limit_usd_ticks = EXCLUDED.monthly_limit_usd_ticks,
    daily_notified_period_start   = CASE WHEN runtime_cost_budget.daily_limit_usd_ticks   IS DISTINCT FROM EXCLUDED.daily_limit_usd_ticks   THEN NULL ELSE runtime_cost_budget.daily_notified_period_start   END,
    weekly_notified_period_start  = CASE WHEN runtime_cost_budget.weekly_limit_usd_ticks  IS DISTINCT FROM EXCLUDED.weekly_limit_usd_ticks  THEN NULL ELSE runtime_cost_budget.weekly_notified_period_start  END,
    monthly_notified_period_start = CASE WHEN runtime_cost_budget.monthly_limit_usd_ticks IS DISTINCT FROM EXCLUDED.monthly_limit_usd_ticks THEN NULL ELSE runtime_cost_budget.monthly_notified_period_start END,
    updated_by = EXCLUDED.updated_by,
    updated_at = now()
RETURNING *;

-- name: DeleteRuntimeCostBudgetsExcept :exec
-- PUT is a full replace: every scope not in keep_keys goes away. The
-- runtime-total row (user_id IS NULL) is addressed by the all-zero uuid so
-- one uuid[] can name both kinds of scope.
DELETE FROM runtime_cost_budget
WHERE runtime_id = $1
  AND COALESCE(user_id, '00000000-0000-0000-0000-000000000000'::uuid) <> ALL(@keep_keys::uuid[]);

-- name: DeleteRuntimeCostBudgetsForRuntime :exec
DELETE FROM runtime_cost_budget WHERE runtime_id = $1;

-- name: DeleteRuntimeCostBudgetsForWorkspaceUser :exec
DELETE FROM runtime_cost_budget WHERE workspace_id = $1 AND user_id = $2;

-- name: ListRuntimeSpendSince :many
-- Spend of one runtime since a cutoff, grouped by provider/model so Go can
-- price the uncosted tokens with the server rate table. owner_user_id narrows
-- the rows to tasks whose agent that user owns (the per-user scope); NULL
-- means the runtime total. Mirrors ListRuntimeUsageByAgent in
-- runtime_usage.sql: cost_usd_ticks is authoritative where present, and the
-- uncosted_* buckets are the tokens from rows without a provider cost.
SELECT
    LOWER(tu.provider) AS provider,
    tu.model,
    COALESCE(SUM(tu.cost_usd_ticks), 0)::bigint AS cost_usd_ticks,
    COALESCE(SUM(tu.input_tokens)       FILTER (WHERE tu.cost_usd_ticks IS NULL), 0)::bigint AS uncosted_input_tokens,
    COALESCE(SUM(tu.output_tokens)      FILTER (WHERE tu.cost_usd_ticks IS NULL), 0)::bigint AS uncosted_output_tokens,
    COALESCE(SUM(tu.cache_read_tokens)  FILTER (WHERE tu.cost_usd_ticks IS NULL), 0)::bigint AS uncosted_cache_read_tokens,
    COALESCE(SUM(tu.cache_write_tokens) FILTER (WHERE tu.cost_usd_ticks IS NULL), 0)::bigint AS uncosted_cache_write_tokens
FROM task_usage tu
JOIN agent_task_queue atq ON atq.id = tu.task_id
LEFT JOIN agent a ON a.id = atq.agent_id
WHERE atq.runtime_id = $1
  AND tu.created_at >= @since::timestamptz
  AND (sqlc.narg('owner_user_id')::uuid IS NULL OR a.owner_id = sqlc.narg('owner_user_id')::uuid)
GROUP BY LOWER(tu.provider), tu.model;

-- name: MarkRuntimeCostBudgetNotified :execrows
-- Claims the "first refusal in this period" notice for one scope and period.
-- Returns 0 rows when the period was already notified, so the caller sends
-- the inbox item only when it wins the claim.
UPDATE runtime_cost_budget SET
    daily_notified_period_start   = CASE WHEN @period::text = 'daily'   THEN @period_start::timestamptz ELSE daily_notified_period_start   END,
    weekly_notified_period_start  = CASE WHEN @period::text = 'weekly'  THEN @period_start::timestamptz ELSE weekly_notified_period_start  END,
    monthly_notified_period_start = CASE WHEN @period::text = 'monthly' THEN @period_start::timestamptz ELSE monthly_notified_period_start END,
    updated_at = now()
WHERE id = @id
  AND (CASE @period::text
         WHEN 'daily'   THEN daily_notified_period_start
         WHEN 'weekly'  THEN weekly_notified_period_start
         ELSE                monthly_notified_period_start
       END) IS DISTINCT FROM @period_start::timestamptz;
```

- [ ] **Step 7: Regenerate sqlc and build**

Run: `make sqlc && cd server && go build ./... && go vet ./pkg/db/...`
Expected: `pkg/db/generated/runtime_cost_budget.sql.go` exists; build clean. If sqlc rejects `ON CONFLICT (runtime_id, user_id)` because it cannot see the NULLS NOT DISTINCT index, keep the SQL and confirm PostgreSQL accepts it in Task 5's handler test (sqlc only type-checks; the conflict target is resolved by the database).

- [ ] **Step 8: Commit**

```bash
git add server/migrations/452_runtime_cost_budget.up.sql server/migrations/452_runtime_cost_budget.down.sql \
        server/migrations/453_runtime_cost_budget_scope_index.up.sql server/migrations/453_runtime_cost_budget_scope_index.down.sql \
        server/cmd/migrate/main.go server/pkg/db/queries/runtime_cost_budget.sql server/pkg/db/generated
git commit -m "feat(db): add runtime_cost_budget table and spend queries"
```

---

### Task 2: `internal/pricing` package with UTC periods and tick estimates

**Files:**
- Move: `server/internal/metrics/pricing.go` → `server/internal/pricing/pricing.go`; `server/internal/metrics/pricing_test.go` → `server/internal/pricing/pricing_test.go`
- Modify: `server/internal/metrics/business.go:591-619` (use `pricing.PriceForModelAlias`, `pricing.TokenCostUSD`, `pricing.CostUSDTicksPerUSD`)
- Create: `server/internal/pricing/period.go`, `period_test.go`, `cost.go`, `cost_test.go`

**Interfaces:**
- Produces `pricing.CostUSDTicksPerUSD = 10_000_000_000`, `pricing.PriceForModelAlias(model string) (ModelPrice, bool)`, `pricing.TokenCostUSD(tokens int64, pricePerM float64) float64`.
- Produces `type Period string` with `PeriodDaily = "daily"`, `PeriodWeekly = "weekly"`, `PeriodMonthly = "monthly"`, `AllPeriods = []Period{...}`, `ParsePeriod(s string) (Period, bool)`.
- Produces `PeriodStart(now time.Time, p Period) time.Time` and `NextPeriodStart(now time.Time, p Period) time.Time`, both UTC.
- Produces `EstimateCostTicks(model string, costTicks, uncostedInput, uncostedOutput, uncostedCacheRead, uncostedCacheWrite int64) int64`, `USDToTicks(usd float64) int64`, `TicksToUSD(ticks int64) float64`.

- [ ] **Step 1: Move the rate table**

```bash
mkdir -p server/internal/pricing
git mv server/internal/metrics/pricing.go server/internal/pricing/pricing.go
git mv server/internal/metrics/pricing_test.go server/internal/pricing/pricing_test.go
```

Edit both moved files: change `package metrics` to `package pricing`. In `pricing.go` rename `tokenCostUSD` to `TokenCostUSD` (exported) and update its one internal reference; keep `PriceForModelAlias`, `ModelPrice`, `CostUSDTicksPerUSD` as they are. In `pricing_test.go` replace any `metrics.` qualifier with none (same package).

- [ ] **Step 2: Point metrics at the new package**

In `server/internal/metrics/business.go` add the import `"github.com/multica-ai/multica/server/internal/pricing"` and replace inside `RecordLLMUsage`:

```go
	price, priced := pricing.PriceForModelAlias(modelAlias)
```
```go
	costs := [4]float64{
		pricing.TokenCostUSD(inputTokens, price.InputPerM),
		pricing.TokenCostUSD(outputTokens, price.OutputPerM),
		pricing.TokenCostUSD(cacheReadTokens, price.CacheReadPerM),
		pricing.TokenCostUSD(cacheWriteTokens, price.CacheWritePerM),
	}
```

Replace the two `CostUSDTicksPerUSD` uses in that function with `pricing.CostUSDTicksPerUSD`. Then run `cd server && grep -rn 'CostUSDTicksPerUSD\|PriceForModelAlias\|tokenCostUSD' internal/metrics` and fix every remaining reference the same way (tests included).

- [ ] **Step 3: Build and run the moved tests**

Run: `cd server && go build ./... && go test ./internal/pricing ./internal/metrics -count=1`
Expected: PASS.

- [ ] **Step 4: Write the failing period tests**

`server/internal/pricing/period_test.go`:

```go
package pricing

import (
	"testing"
	"time"
)

func TestPeriodStartIsUTCCalendarBoundary(t *testing.T) {
	// Wednesday 2026-09-02 15:04:05 UTC.
	now := time.Date(2026, 9, 2, 15, 4, 5, 0, time.UTC)
	cases := []struct {
		period    Period
		wantStart time.Time
		wantNext  time.Time
	}{
		{PeriodDaily, time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC), time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC)},
		{PeriodWeekly, time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC), time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC)},
		{PeriodMonthly, time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)},
	}
	for _, tc := range cases {
		if got := PeriodStart(now, tc.period); !got.Equal(tc.wantStart) {
			t.Errorf("PeriodStart(%s) = %s, want %s", tc.period, got, tc.wantStart)
		}
		if got := NextPeriodStart(now, tc.period); !got.Equal(tc.wantNext) {
			t.Errorf("NextPeriodStart(%s) = %s, want %s", tc.period, got, tc.wantNext)
		}
	}
}

func TestPeriodStartIgnoresCallerLocation(t *testing.T) {
	shanghai := time.FixedZone("Asia/Shanghai", 8*3600)
	// 2026-09-03 06:00 in Shanghai is 2026-09-02 22:00 UTC: the UTC day is
	// still the 2nd, so the daily window must not have rolled over.
	now := time.Date(2026, 9, 3, 6, 0, 0, 0, shanghai)
	if got := PeriodStart(now, PeriodDaily); !got.Equal(time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("daily start = %s, want 2026-09-02T00:00Z", got)
	}
}

func TestWeeklyPeriodStartsOnMondayAcrossMonthAndYearEnds(t *testing.T) {
	// Sunday 2027-01-03: the week began Monday 2026-12-28.
	now := time.Date(2027, 1, 3, 12, 0, 0, 0, time.UTC)
	if got := PeriodStart(now, PeriodWeekly); !got.Equal(time.Date(2026, 12, 28, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("weekly start = %s, want 2026-12-28", got)
	}
	// Monday itself starts its own week.
	monday := time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC)
	if got := PeriodStart(monday, PeriodWeekly); !got.Equal(monday) {
		t.Fatalf("weekly start on a Monday = %s, want the same day", got)
	}
}

func TestMonthlyNextPeriodHandlesDecember(t *testing.T) {
	now := time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC)
	if got := NextPeriodStart(now, PeriodMonthly); !got.Equal(time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("next monthly = %s, want 2027-01-01", got)
	}
}

func TestParsePeriod(t *testing.T) {
	for _, s := range []string{"daily", "weekly", "monthly"} {
		if p, ok := ParsePeriod(s); !ok || string(p) != s {
			t.Fatalf("ParsePeriod(%q) = %q, %v", s, p, ok)
		}
	}
	if _, ok := ParsePeriod("yearly"); ok {
		t.Fatal("ParsePeriod accepted an unknown period")
	}
}
```

- [ ] **Step 5: Run to verify failure**

Run: `cd server && go test ./internal/pricing -run 'Period' -count=1`
Expected: FAIL, `undefined: Period`.

- [ ] **Step 6: Implement period.go**

`server/internal/pricing/period.go`:

```go
package pricing

import "time"

// Period is one budget window. Boundaries are always computed in UTC, whatever
// the caller's location: a day is the UTC calendar day, a week starts Monday
// 00:00 UTC and a month starts on the 1st at 00:00 UTC.
type Period string

const (
	PeriodDaily   Period = "daily"
	PeriodWeekly  Period = "weekly"
	PeriodMonthly Period = "monthly"
)

// AllPeriods lists the periods in display order.
var AllPeriods = []Period{PeriodDaily, PeriodWeekly, PeriodMonthly}

// ParsePeriod accepts the wire spelling of a period.
func ParsePeriod(s string) (Period, bool) {
	switch Period(s) {
	case PeriodDaily, PeriodWeekly, PeriodMonthly:
		return Period(s), true
	default:
		return "", false
	}
}

// PeriodStart returns the UTC start of the period containing now.
func PeriodStart(now time.Time, p Period) time.Time {
	u := now.UTC()
	y, m, d := u.Date()
	switch p {
	case PeriodWeekly:
		// time.Weekday counts Sunday as 0; shift so Monday is day 0.
		daysSinceMonday := (int(u.Weekday()) + 6) % 7
		return time.Date(y, m, d-daysSinceMonday, 0, 0, 0, 0, time.UTC)
	case PeriodMonthly:
		return time.Date(y, m, 1, 0, 0, 0, 0, time.UTC)
	default:
		return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	}
}

// NextPeriodStart returns the UTC instant the current period resets.
func NextPeriodStart(now time.Time, p Period) time.Time {
	start := PeriodStart(now, p)
	switch p {
	case PeriodWeekly:
		return start.AddDate(0, 0, 7)
	case PeriodMonthly:
		return start.AddDate(0, 1, 0)
	default:
		return start.AddDate(0, 0, 1)
	}
}
```

- [ ] **Step 7: Run the period tests**

Run: `cd server && go test ./internal/pricing -run 'Period' -count=1`
Expected: PASS.

- [ ] **Step 8: Write the failing cost tests**

`server/internal/pricing/cost_test.go`:

```go
package pricing

import "testing"

func TestEstimateCostTicksPricesUncostedTokensOfKnownModel(t *testing.T) {
	// claude-sonnet-5: input 2.00, output 10.00, cache read 0.20, cache write 2.50 per 1M.
	got := EstimateCostTicks("claude-sonnet-5", 0, 1_000_000, 100_000, 500_000, 200_000)
	// 2.00 + 1.00 + 0.10 + 0.50 = 3.60 USD.
	if want := USDToTicks(3.60); got != want {
		t.Fatalf("ticks = %d, want %d", got, want)
	}
}

func TestEstimateCostTicksKeepsProviderCostAndIgnoresUnpricedTokens(t *testing.T) {
	got := EstimateCostTicks("some-unknown-model", 1_500, 1_000_000, 1_000_000, 0, 0)
	if got != 1_500 {
		t.Fatalf("ticks = %d, want the provider cost 1500 only", got)
	}
}

func TestEstimateCostTicksAddsProviderCostToEstimate(t *testing.T) {
	got := EstimateCostTicks("claude-sonnet-5", USDToTicks(1), 1_000_000, 0, 0, 0)
	if want := USDToTicks(3); got != want {
		t.Fatalf("ticks = %d, want %d", got, want)
	}
}

func TestUSDTicksRoundTrip(t *testing.T) {
	if USDToTicks(20) != 200_000_000_000 {
		t.Fatalf("USDToTicks(20) = %d", USDToTicks(20))
	}
	if TicksToUSD(200_000_000_000) != 20 {
		t.Fatalf("TicksToUSD = %v", TicksToUSD(200_000_000_000))
	}
	// Two-decimal amounts must survive float rounding.
	if TicksToUSD(USDToTicks(0.07)) != 0.07 {
		t.Fatalf("0.07 round trip = %v", TicksToUSD(USDToTicks(0.07)))
	}
}
```

- [ ] **Step 9: Run to verify failure**

Run: `cd server && go test ./internal/pricing -run 'Cost|USD' -count=1`
Expected: FAIL, `undefined: EstimateCostTicks`.

- [ ] **Step 10: Implement cost.go**

`server/internal/pricing/cost.go`:

```go
package pricing

import "math"

// USDToTicks converts a dollar amount to 1e-10 USD ticks, rounding to the
// nearest tick so two-decimal inputs are exact.
func USDToTicks(usd float64) int64 {
	return int64(math.Round(usd * CostUSDTicksPerUSD))
}

// TicksToUSD is the inverse of USDToTicks.
func TicksToUSD(ticks int64) float64 {
	return float64(ticks) / CostUSDTicksPerUSD
}

// EstimateCostTicks is the single server-side cost formula for budgets:
// the provider-reported cost (authoritative where present) plus the rate-table
// estimate for the tokens the provider did not price. A model without a rate
// row contributes only its provider cost; its uncosted tokens count as zero,
// which is why budget spend is a lower bound.
func EstimateCostTicks(model string, costTicks, uncostedInput, uncostedOutput, uncostedCacheRead, uncostedCacheWrite int64) int64 {
	price, ok := PriceForModelAlias(model)
	if !ok {
		return costTicks
	}
	usd := TokenCostUSD(uncostedInput, price.InputPerM) +
		TokenCostUSD(uncostedOutput, price.OutputPerM) +
		TokenCostUSD(uncostedCacheRead, price.CacheReadPerM) +
		TokenCostUSD(uncostedCacheWrite, price.CacheWritePerM)
	return costTicks + USDToTicks(usd)
}
```

- [ ] **Step 11: Run all pricing tests and vet**

Run: `cd server && go test ./internal/pricing ./internal/metrics -count=1 && go vet ./internal/pricing ./internal/metrics`
Expected: PASS.

- [ ] **Step 12: Commit**

```bash
git add server/internal/pricing server/internal/metrics
git commit -m "refactor(pricing): move the rate table into internal/pricing with UTC periods"
```

---

### Task 3: Budget check at enqueue time

**Files:**
- Modify: `server/internal/dispatch/reason.go` (after `ReasonIssueLimitReached`)
- Create: `server/internal/service/runtime_cost_budget.go`, `server/internal/service/runtime_cost_budget_test.go`
- Modify: `server/internal/service/task.go` — `enqueueIssueTaskWithCommentPlan` (~line 1240, after `applyAttributionFallback`), `enqueueMentionTaskWithCommentPlan` (~line 1394), `enqueueQuickCreateTask` (~line 1596, after the `RuntimeID` check), `enqueueChatTaskTx` (~line 2010, after the `RuntimeID` check, using `qtx`)
- Modify: `server/internal/service/autopilot.go:626-632` (`dispatchFailReasonCode`)
- Modify: `server/internal/handler/admission.go` (constants block ~line 48 and `dispatchBlockedFallbackMessage`), `server/internal/handler/comment.go:2271-2276` (`commentEnqueueFailureReason`), `server/internal/handler/issue.go:2670-2678` (quick create), `server/internal/handler/chat.go:950-962`

**Interfaces:**
- Consumes `pricing.PeriodStart`, `pricing.NextPeriodStart`, `pricing.EstimateCostTicks`, `pricing.AllPeriods`; sqlc `ListRuntimeCostBudgets`, `ListRuntimeSpendSince`.
- Produces `dispatch.ReasonBudgetExceeded ReasonCode = "budget_exceeded"`.
- Produces in package `service`:

```go
type RuntimeBudgetScope string // "runtime" | "user"

type RuntimeBudgetExceededError struct {
	Scope       RuntimeBudgetScope
	Period      pricing.Period
	RuntimeID   pgtype.UUID
	UserID      pgtype.UUID // valid only for ScopeUser
	UsedTicks   int64
	LimitTicks  int64
	PeriodStart time.Time
	ResetAt     time.Time
}
func (e *RuntimeBudgetExceededError) Error() string

// checkRuntimeCostBudget refuses the enqueue with *RuntimeBudgetExceededError
// when any configured limit for the agent's runtime (total, or the agent
// owner's row) is reached. Nil when no budget rows exist.
func (s *TaskService) checkRuntimeCostBudget(ctx context.Context, q *db.Queries, agent db.Agent, now time.Time) error

// runtimeSpendTicks sums priced spend for one scope since periodStart.
func runtimeSpendTicks(ctx context.Context, q *db.Queries, runtimeID, ownerUserID pgtype.UUID, since time.Time) (int64, error)
```

- Note: `enqueueRerunTask` delegates to `enqueueMentionTaskWithCommentPlan`, so it is covered by that hook.

- [ ] **Step 1: Add the dispatch reason**

In `server/internal/dispatch/reason.go`, after the `ReasonIssueLimitReached` declaration:

```go
	// ReasonBudgetExceeded: the target's runtime has a cost budget (total or
	// for the agent owner) whose current period is spent. The run is not
	// queued; the user triggers it again after the period resets. Reveals
	// nothing about a private target: the caller already sees the runtime.
	ReasonBudgetExceeded ReasonCode = "budget_exceeded"
```

In `server/internal/handler/admission.go` add to the aliased constants block:

```go
	ReasonBudgetExceeded        = dispatch.ReasonBudgetExceeded
```

and in `dispatchBlockedFallbackMessage` before `default:`:

```go
	case ReasonBudgetExceeded:
		return "the cost budget for this runtime is reached"
```

- [ ] **Step 2: Write the failing service test**

`server/internal/service/runtime_cost_budget_test.go` (DB-backed; reuses `newResolveOriginatorPool` and `seedAttributionFixture` from the sibling attribution tests, which create a workspace, a member user, a runtime owned by that user and an agent owned by that user):

```go
package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/pricing"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// seedSpend records one completed task on the fixture agent with a
// provider-priced usage row worth usd dollars, created at createdAt.
func seedSpend(t *testing.T, ctx context.Context, pool *pgxpool.Pool, agentID, issueID string, usd float64, createdAt time.Time) {
	t.Helper()
	var taskID string
	err := pool.QueryRow(ctx, `
		INSERT INTO agent_task_queue (agent_id, runtime_id, issue_id, status, priority, originator_source, completed_at)
		VALUES ($1, (SELECT runtime_id FROM agent WHERE id = $1), $2, 'completed', 0, 'delegation', $3)
		RETURNING id`, agentID, issueID, createdAt).Scan(&taskID)
	if err != nil {
		t.Fatalf("seed task: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM agent_task_queue WHERE id = $1`, taskID) })
	if _, err := pool.Exec(ctx, `
		INSERT INTO task_usage (task_id, provider, model, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, cost_usd_ticks, created_at)
		VALUES ($1, 'xai', 'grok-4.5', 0, 0, 0, 0, $2, $3)`, taskID, pricing.USDToTicks(usd), createdAt); err != nil {
		t.Fatalf("seed usage: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM task_usage WHERE task_id = $1`, taskID) })
}

func seedBudget(t *testing.T, ctx context.Context, q *db.Queries, workspaceID, runtimeID string, userID *string, daily, weekly, monthly *float64) db.RuntimeCostBudget {
	t.Helper()
	toTicks := func(v *float64) pgtype.Int8 {
		if v == nil {
			return pgtype.Int8{}
		}
		return pgtype.Int8{Int64: pricing.USDToTicks(*v), Valid: true}
	}
	params := db.UpsertRuntimeCostBudgetParams{
		ID:                   util.MustParseUUID(newAutopilotIdempotencyKey()),
		WorkspaceID:          util.MustParseUUID(workspaceID),
		RuntimeID:            util.MustParseUUID(runtimeID),
		DailyLimitUsdTicks:   toTicks(daily),
		WeeklyLimitUsdTicks:  toTicks(weekly),
		MonthlyLimitUsdTicks: toTicks(monthly),
	}
	if userID != nil {
		params.UserID = util.MustParseUUID(*userID)
	}
	row, err := q.UpsertRuntimeCostBudget(ctx, params)
	if err != nil {
		t.Fatalf("seed budget: %v", err)
	}
	t.Cleanup(func() { _ = q.DeleteRuntimeCostBudgetsForRuntime(ctx, row.RuntimeID) })
	return row
}

func TestCheckRuntimeCostBudgetPassesWithoutRows(t *testing.T) {
	pool := newResolveOriginatorPool(t)
	ctx := context.Background()
	q := db.New(pool)
	_, _, agentID, _ := seedAttributionFixture(t, pool)
	agent, err := q.GetAgent(ctx, util.MustParseUUID(agentID))
	if err != nil {
		t.Fatal(err)
	}
	svc := &TaskService{Queries: q, TxStarter: pool, Bus: events.New()}
	if err := svc.checkRuntimeCostBudget(ctx, q, agent, time.Now()); err != nil {
		t.Fatalf("expected nil without budget rows, got %v", err)
	}
}

func TestCheckRuntimeCostBudgetRefusesWhenRuntimeTotalReached(t *testing.T) {
	pool := newResolveOriginatorPool(t)
	ctx := context.Background()
	q := db.New(pool)
	workspaceID, _, agentID, issueID := seedAttributionFixture(t, pool)
	agent, _ := q.GetAgent(ctx, util.MustParseUUID(agentID))
	runtimeID := util.UUIDToString(agent.RuntimeID)
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	daily := 20.0
	seedBudget(t, ctx, q, workspaceID, runtimeID, nil, &daily, nil, nil)
	seedSpend(t, ctx, pool, agentID, issueID, 20.31, now.Add(-time.Hour))

	svc := &TaskService{Queries: q, TxStarter: pool, Bus: events.New()}
	err := svc.checkRuntimeCostBudget(ctx, q, agent, now)
	var exceeded *RuntimeBudgetExceededError
	if !errors.As(err, &exceeded) {
		t.Fatalf("expected RuntimeBudgetExceededError, got %v", err)
	}
	if exceeded.Scope != RuntimeBudgetScopeRuntime || exceeded.Period != pricing.PeriodDaily {
		t.Fatalf("scope/period = %s/%s", exceeded.Scope, exceeded.Period)
	}
	if exceeded.UsedTicks != pricing.USDToTicks(20.31) || exceeded.LimitTicks != pricing.USDToTicks(20) {
		t.Fatalf("used/limit = %d/%d", exceeded.UsedTicks, exceeded.LimitTicks)
	}
	if !exceeded.ResetAt.Equal(time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("reset_at = %s", exceeded.ResetAt)
	}
}

func TestCheckRuntimeCostBudgetIgnoresSpendBeforePeriodStart(t *testing.T) {
	pool := newResolveOriginatorPool(t)
	ctx := context.Background()
	q := db.New(pool)
	workspaceID, _, agentID, issueID := seedAttributionFixture(t, pool)
	agent, _ := q.GetAgent(ctx, util.MustParseUUID(agentID))
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	daily := 20.0
	seedBudget(t, ctx, q, workspaceID, util.UUIDToString(agent.RuntimeID), nil, &daily, nil, nil)
	// Yesterday's spend belongs to yesterday's UTC day.
	seedSpend(t, ctx, pool, agentID, issueID, 50, now.Add(-13*time.Hour))
	svc := &TaskService{Queries: q, TxStarter: pool, Bus: events.New()}
	if err := svc.checkRuntimeCostBudget(ctx, q, agent, now); err != nil {
		t.Fatalf("expected pass, got %v", err)
	}
}

func TestCheckRuntimeCostBudgetPerUserBlocksOnlyThatOwner(t *testing.T) {
	pool := newResolveOriginatorPool(t)
	ctx := context.Background()
	q := db.New(pool)
	workspaceID, ownerID, agentID, issueID := seedAttributionFixture(t, pool)
	agent, _ := q.GetAgent(ctx, util.MustParseUUID(agentID))
	runtimeID := util.UUIDToString(agent.RuntimeID)
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	weekly := 10.0
	seedBudget(t, ctx, q, workspaceID, runtimeID, &ownerID, nil, &weekly, nil)
	seedSpend(t, ctx, pool, agentID, issueID, 12, now.Add(-24*time.Hour))

	svc := &TaskService{Queries: q, TxStarter: pool, Bus: events.New()}
	var exceeded *RuntimeBudgetExceededError
	if err := svc.checkRuntimeCostBudget(ctx, q, agent, now); !errors.As(err, &exceeded) || exceeded.Scope != RuntimeBudgetScopeUser {
		t.Fatalf("expected user-scope refusal, got %v", err)
	}

	// A second agent on the same runtime owned by nobody is not capped by the
	// owner's row.
	var otherAgentID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO agent (workspace_id, name, runtime_mode, runtime_config, runtime_id, visibility, max_concurrent_tasks, instructions, custom_env, custom_args)
		VALUES ($1, 'budget-other', 'cloud', '{}'::jsonb, $2, 'public', 1, '', '{}'::jsonb, '{}'::jsonb)
		RETURNING id`, workspaceID, runtimeID).Scan(&otherAgentID); err != nil {
		t.Fatalf("seed other agent: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM agent WHERE id = $1`, otherAgentID) })
	other, _ := q.GetAgent(ctx, util.MustParseUUID(otherAgentID))
	if err := svc.checkRuntimeCostBudget(ctx, q, other, now); err != nil {
		t.Fatalf("other owner must pass, got %v", err)
	}
}

func TestCheckRuntimeCostBudgetUnpricedTokensCountAsZero(t *testing.T) {
	pool := newResolveOriginatorPool(t)
	ctx := context.Background()
	q := db.New(pool)
	workspaceID, _, agentID, issueID := seedAttributionFixture(t, pool)
	agent, _ := q.GetAgent(ctx, util.MustParseUUID(agentID))
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	daily := 1.0
	seedBudget(t, ctx, q, workspaceID, util.UUIDToString(agent.RuntimeID), nil, &daily, nil, nil)
	var taskID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO agent_task_queue (agent_id, runtime_id, issue_id, status, priority, originator_source, completed_at)
		VALUES ($1, (SELECT runtime_id FROM agent WHERE id = $1), $2, 'completed', 0, 'delegation', $3)
		RETURNING id`, agentID, issueID, now.Add(-time.Hour)).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM agent_task_queue WHERE id = $1`, taskID) })
	if _, err := pool.Exec(ctx, `
		INSERT INTO task_usage (task_id, provider, model, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, created_at)
		VALUES ($1, 'copilot', 'model-nobody-prices', 50000000, 50000000, 0, 0, $2)`, taskID, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM task_usage WHERE task_id = $1`, taskID) })
	svc := &TaskService{Queries: q, TxStarter: pool, Bus: events.New()}
	if err := svc.checkRuntimeCostBudget(ctx, q, agent, now); err != nil {
		t.Fatalf("unpriced usage must not count, got %v", err)
	}
}
```

- [ ] **Step 3: Run to verify failure**

Run: `cd server && go test ./internal/service -run 'CheckRuntimeCostBudget' -count=1`
Expected: FAIL, `undefined: RuntimeBudgetExceededError` (or `checkRuntimeCostBudget`).

- [ ] **Step 4: Implement the service**

`server/internal/service/runtime_cost_budget.go`:

```go
package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/pricing"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// RuntimeBudgetScope names which row refused a run.
type RuntimeBudgetScope string

const (
	RuntimeBudgetScopeRuntime RuntimeBudgetScope = "runtime"
	RuntimeBudgetScopeUser    RuntimeBudgetScope = "user"
)

// RuntimeBudgetExceededError is returned by the enqueue helpers when a
// configured limit for the target runtime is already spent. Handlers map it to
// dispatch.ReasonBudgetExceeded with errors.As; it is never matched by string.
type RuntimeBudgetExceededError struct {
	Scope       RuntimeBudgetScope
	Period      pricing.Period
	RuntimeID   pgtype.UUID
	UserID      pgtype.UUID
	UsedTicks   int64
	LimitTicks  int64
	PeriodStart time.Time
	ResetAt     time.Time
}

func (e *RuntimeBudgetExceededError) Error() string {
	return fmt.Sprintf("runtime cost budget exceeded: %s %s limit %.2f USD, used %.2f USD, resets %s",
		e.Scope, e.Period, pricing.TicksToUSD(e.LimitTicks), pricing.TicksToUSD(e.UsedTicks), e.ResetAt.UTC().Format(time.RFC3339))
}

// budgetLimit returns the configured limit of one period on a row, or false.
func budgetLimit(row db.RuntimeCostBudget, p pricing.Period) (int64, bool) {
	var v pgtype.Int8
	switch p {
	case pricing.PeriodDaily:
		v = row.DailyLimitUsdTicks
	case pricing.PeriodWeekly:
		v = row.WeeklyLimitUsdTicks
	case pricing.PeriodMonthly:
		v = row.MonthlyLimitUsdTicks
	}
	return v.Int64, v.Valid
}

// runtimeSpendTicks sums the priced spend of one scope since `since`.
// ownerUserID invalid means the runtime total.
func runtimeSpendTicks(ctx context.Context, q *db.Queries, runtimeID, ownerUserID pgtype.UUID, since time.Time) (int64, error) {
	rows, err := q.ListRuntimeSpendSince(ctx, db.ListRuntimeSpendSinceParams{
		RuntimeID:   runtimeID,
		Since:       pgtype.Timestamptz{Time: since, Valid: true},
		OwnerUserID: ownerUserID,
	})
	if err != nil {
		return 0, fmt.Errorf("list runtime spend: %w", err)
	}
	var total int64
	for _, r := range rows {
		total += pricing.EstimateCostTicks(r.Model, r.CostUsdTicks,
			r.UncostedInputTokens, r.UncostedOutputTokens, r.UncostedCacheReadTokens, r.UncostedCacheWriteTokens)
	}
	return total, nil
}

// evaluateBudgetRow checks every configured period of one row and returns the
// first reached limit. spendFor is injected so callers can share one spend
// lookup per (row, period) or memoise across rows.
func evaluateBudgetRow(ctx context.Context, q *db.Queries, row db.RuntimeCostBudget, scope RuntimeBudgetScope, now time.Time) (*RuntimeBudgetExceededError, error) {
	for _, p := range pricing.AllPeriods {
		limit, ok := budgetLimit(row, p)
		if !ok {
			continue
		}
		start := pricing.PeriodStart(now, p)
		used, err := runtimeSpendTicks(ctx, q, row.RuntimeID, row.UserID, start)
		if err != nil {
			return nil, err
		}
		if used >= limit {
			return &RuntimeBudgetExceededError{
				Scope: scope, Period: p, RuntimeID: row.RuntimeID, UserID: row.UserID,
				UsedTicks: used, LimitTicks: limit, PeriodStart: start, ResetAt: pricing.NextPeriodStart(now, p),
			}, nil
		}
	}
	return nil, nil
}

// checkRuntimeCostBudget refuses the enqueue when the agent's runtime total or
// the agent owner's per-user budget is spent for the current UTC period. It
// runs after attribution in every enqueue helper. Workspaces without budgets
// pay one indexed lookup and return immediately. A database error is returned
// as-is so the enqueue fails closed rather than silently spending.
func (s *TaskService) checkRuntimeCostBudget(ctx context.Context, q *db.Queries, agent db.Agent, now time.Time) error {
	if !agent.RuntimeID.Valid {
		return nil
	}
	rows, err := q.ListRuntimeCostBudgets(ctx, agent.RuntimeID)
	if err != nil {
		return fmt.Errorf("list runtime cost budgets: %w", err)
	}
	if len(rows) == 0 {
		return nil
	}
	for _, row := range rows {
		scope := RuntimeBudgetScopeUser
		if !row.UserID.Valid {
			scope = RuntimeBudgetScopeRuntime
		} else if !agent.OwnerID.Valid || util.UUIDToString(row.UserID) != util.UUIDToString(agent.OwnerID) {
			continue
		}
		exceeded, err := evaluateBudgetRow(ctx, q, row, scope, now)
		if err != nil {
			return err
		}
		if exceeded != nil {
			slog.Info("task enqueue refused: runtime cost budget reached",
				"runtime_id", util.UUIDToString(row.RuntimeID), "scope", scope, "period", exceeded.Period,
				"used_ticks", exceeded.UsedTicks, "limit_ticks", exceeded.LimitTicks)
			s.notifyRuntimeBudgetExceeded(ctx, q, row, exceeded)
			return exceeded
		}
	}
	return nil
}

// notifyRuntimeBudgetExceeded is implemented in Task 4. Until then it is a
// no-op so the check can ship on its own.
func (s *TaskService) notifyRuntimeBudgetExceeded(ctx context.Context, q *db.Queries, row db.RuntimeCostBudget, exceeded *RuntimeBudgetExceededError) {
}
```

- [ ] **Step 5: Run the service tests**

Run: `cd server && go test ./internal/service -run 'CheckRuntimeCostBudget' -count=1`
Expected: PASS.

- [ ] **Step 6: Hook the check into the enqueue helpers**

In `server/internal/service/task.go`:

`enqueueIssueTaskWithCommentPlan`, immediately after the `applyAttributionFallback` error return:

```go
	if err := s.checkRuntimeCostBudget(ctx, s.Queries, agent, time.Now()); err != nil {
		return db.AgentTaskQueue{}, err
	}
```

`enqueueMentionTaskWithCommentPlan`, immediately after its `applyAttributionFallback` error return: the same three lines.

`enqueueQuickCreateTask`, immediately after the `if !agent.RuntimeID.Valid { ... }` block: the same three lines.

`enqueueChatTaskTx`, immediately after its `if !agent.RuntimeID.Valid { ... }` block, using the transaction handle:

```go
	if err := s.checkRuntimeCostBudget(ctx, qtx, agent, time.Now()); err != nil {
		return db.AgentTaskQueue{}, err
	}
```

If `agent` in `enqueueChatTaskTx` is a `GetAgentForClaimUpdate` row type rather than `db.Agent`, build the argument as `db.Agent{ID: agent.ID, RuntimeID: agent.RuntimeID, OwnerID: agent.OwnerID}` — the check reads only those three fields.

- [ ] **Step 7: Write the end-to-end enqueue refusal test**

Append to `server/internal/service/runtime_cost_budget_test.go`:

```go
func TestEnqueueTaskForIssueRefusedWhenBudgetReached(t *testing.T) {
	pool := newResolveOriginatorPool(t)
	ctx := context.Background()
	q := db.New(pool)
	workspaceID, creatorID, agentID, issueID := seedAttributionFixture(t, pool)
	agent, _ := q.GetAgent(ctx, util.MustParseUUID(agentID))
	daily := 5.0
	seedBudget(t, ctx, q, workspaceID, util.UUIDToString(agent.RuntimeID), nil, &daily, nil, nil)
	seedSpend(t, ctx, pool, agentID, issueID, 5, time.Now().Add(-time.Minute))

	issue := db.Issue{
		ID: util.MustParseUUID(issueID), AssigneeID: util.MustParseUUID(agentID),
		Priority: "medium", CreatorType: "member", CreatorID: util.MustParseUUID(creatorID),
		WorkspaceID: util.MustParseUUID(workspaceID),
		AssigneeType: pgtype.Text{String: "agent", Valid: true},
	}
	svc := &TaskService{Queries: q, TxStarter: pool, Bus: events.New()}
	_, err := svc.EnqueueTaskForIssue(ctx, issue)
	var exceeded *RuntimeBudgetExceededError
	if !errors.As(err, &exceeded) {
		t.Fatalf("expected budget refusal, got %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM agent_task_queue WHERE issue_id = $1 AND status = 'queued'`, issueID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("refused run must not be queued, found %d", n)
	}
}
```

Run: `cd server && go test ./internal/service -run 'RuntimeCostBudget|EnqueueTaskForIssueRefused' -count=1`
Expected: PASS.

- [ ] **Step 8: Map the error to the reason code in handlers**

`server/internal/handler/comment.go`, `commentEnqueueFailureReason`:

```go
func commentEnqueueFailureReason(err error) DispatchReasonCode {
	if errors.Is(err, service.ErrAttributionFailClosed) {
		return ReasonAttributionBlocked
	}
	var budget *service.RuntimeBudgetExceededError
	if errors.As(err, &budget) {
		return ReasonBudgetExceeded
	}
	return ReasonInternalError
}
```

Find the merge-path counterpart: in `comment.go` around line 2404 where `errors.Is(err, service.ErrAttributionFailClosed)` returns `commentMergeAttributionBlocked`, add a `commentMergeBudgetExceeded` result constant next to `commentMergeAttributionBlocked`, return it under the same `errors.As` check, and extend `commentMergeTerminalOutcome` with:

```go
	case commentMergeBudgetExceeded:
		return DispatchBlocked, ReasonBudgetExceeded, true
```

`server/internal/service/autopilot.go`, `dispatchFailReasonCode`:

```go
func dispatchFailReasonCode(err error) dispatch.ReasonCode {
	if errors.Is(err, ErrAttributionFailClosed) {
		return dispatch.ReasonAttributionBlocked
	}
	var budget *RuntimeBudgetExceededError
	if errors.As(err, &budget) {
		return dispatch.ReasonBudgetExceeded
	}
	return dispatch.ReasonInternalError
}
```

`server/internal/handler/issue.go`, quick create, before the `writeIssueLimitReached` check:

```go
		var budget *service.RuntimeBudgetExceededError
		if errors.As(err, &budget) {
			h.writeDispatchBlocked(w, http.StatusConflict, ReasonBudgetExceeded)
			return
		}
```

`server/internal/handler/chat.go`, in the `switch` after `SendDirectChatMessage`, add before `default:`:

```go
		case func() bool { var b *service.RuntimeBudgetExceededError; return errors.As(err, &b) }():
			h.writeDispatchBlocked(w, http.StatusConflict, ReasonBudgetExceeded)
```

(If the file style prefers it, declare `var budgetErr *service.RuntimeBudgetExceededError` above the switch and use `case errors.As(err, &budgetErr):`.)

- [ ] **Step 9: Build, vet, run handler unit tests that do not need a DB**

Run: `cd server && go build ./... && go vet ./internal/handler ./internal/service && go test ./internal/handler -run 'Admission|Dispatch|Reason' -count=1`
Expected: PASS.

- [ ] **Step 10: Commit**

```bash
git add server/internal/dispatch/reason.go server/internal/service/runtime_cost_budget.go server/internal/service/runtime_cost_budget_test.go \
        server/internal/service/task.go server/internal/service/autopilot.go \
        server/internal/handler/admission.go server/internal/handler/comment.go server/internal/handler/issue.go server/internal/handler/chat.go
git commit -m "feat(budget): refuse enqueue when a runtime cost budget is reached"
```

---

### Task 4: Inbox notice on the first refusal of a period

**Files:**
- Modify: `server/internal/service/runtime_cost_budget.go` (replace the `notifyRuntimeBudgetExceeded` stub)
- Modify: `server/internal/service/runtime_cost_budget_test.go`

**Interfaces:**
- Consumes sqlc `MarkRuntimeCostBudgetNotified`, `CreateInboxItem`, `GetAgentRuntime`; `events.Bus.Publish`; `protocol.EventInboxNew`; `dbid.NewV7()`.
- Produces inbox items of `Type: "runtime_budget_exceeded"`, `Severity: "attention"`, `RecipientType: "member"`, `Details` JSON:

```json
{"scope":"user|runtime","period":"daily","runtime_id":"...","user_id":"..."|null,
 "used_usd":20.31,"limit_usd":20,"period_start":"RFC3339","reset_at":"RFC3339"}
```

- [ ] **Step 1: Write the failing notification test**

Append to `server/internal/service/runtime_cost_budget_test.go`:

```go
func TestRuntimeBudgetNoticeFiresOncePerPeriod(t *testing.T) {
	pool := newResolveOriginatorPool(t)
	ctx := context.Background()
	q := db.New(pool)
	workspaceID, ownerID, agentID, issueID := seedAttributionFixture(t, pool)
	agent, _ := q.GetAgent(ctx, util.MustParseUUID(agentID))
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	daily := 5.0
	row := seedBudget(t, ctx, q, workspaceID, util.UUIDToString(agent.RuntimeID), nil, &daily, nil, nil)
	seedSpend(t, ctx, pool, agentID, issueID, 6, now.Add(-time.Hour))
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM inbox_item WHERE workspace_id = $1 AND type = 'runtime_budget_exceeded'`, workspaceID)
	})

	svc := &TaskService{Queries: q, TxStarter: pool, Bus: events.New()}
	for i := 0; i < 2; i++ {
		if err := svc.checkRuntimeCostBudget(ctx, q, agent, now); err == nil {
			t.Fatal("expected refusal")
		}
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM inbox_item WHERE workspace_id = $1 AND type = 'runtime_budget_exceeded' AND recipient_id = $2`, workspaceID, ownerID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("runtime owner notices = %d, want exactly 1", n)
	}
	// Next day: yesterday's spend is outside the new UTC day, so the check
	// passes until today's spend lands; then the marker no longer matches and
	// the notice fires again.
	if err := svc.checkRuntimeCostBudget(ctx, q, agent, now.Add(24*time.Hour)); err != nil {
		t.Fatalf("expected pass at the start of the next day, got %v", err)
	}
	seedSpend(t, ctx, pool, agentID, issueID, 6, now.Add(25*time.Hour))
	if err := svc.checkRuntimeCostBudget(ctx, q, agent, now.Add(26*time.Hour)); err == nil {
		t.Fatal("expected refusal on the next day")
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM inbox_item WHERE workspace_id = $1 AND type = 'runtime_budget_exceeded'`, workspaceID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("notices after period rollover = %d, want 2", n)
	}
	_ = row
}
```

If the inbox table is not named `inbox_item`, read `server/pkg/db/queries/inbox.sql` for the table name used by `CreateInboxItem` and adjust the two SQL strings.

- [ ] **Step 2: Run to verify failure**

Run: `cd server && go test ./internal/service -run 'RuntimeBudgetNoticeFiresOncePerPeriod' -count=1`
Expected: FAIL on `runtime owner notices = 0, want exactly 1`.

- [ ] **Step 3: Implement the notification**

Replace the stub in `server/internal/service/runtime_cost_budget.go` and add the imports `encoding/json`, `github.com/multica-ai/multica/server/internal/dbid`, `github.com/multica-ai/multica/server/internal/events`, `github.com/multica-ai/multica/server/pkg/protocol` (use the same import paths the sibling file `autopilot_quota_notifications.go` uses for `dbid`, `events`, `protocol`):

```go
// notifyRuntimeBudgetExceeded creates one "limit reached" inbox item per
// period for the scope that refused the run. MarkRuntimeCostBudgetNotified
// claims the period first, so concurrent refusals produce one notice.
// Recipients: the blocked user (per-user scope) and the runtime owner,
// de-duplicated. Failures are logged; the refusal itself is already decided.
func (s *TaskService) notifyRuntimeBudgetExceeded(ctx context.Context, q *db.Queries, row db.RuntimeCostBudget, exceeded *RuntimeBudgetExceededError) {
	claimed, err := q.MarkRuntimeCostBudgetNotified(ctx, db.MarkRuntimeCostBudgetNotifiedParams{
		Period:      string(exceeded.Period),
		PeriodStart: pgtype.Timestamptz{Time: exceeded.PeriodStart, Valid: true},
		ID:          row.ID,
	})
	if err != nil {
		slog.Warn("runtime budget notice claim failed", "budget_id", util.UUIDToString(row.ID), "error", err)
		return
	}
	if claimed == 0 {
		return
	}
	rt, err := q.GetAgentRuntime(ctx, row.RuntimeID)
	if err != nil {
		slog.Warn("runtime budget notice: load runtime failed", "runtime_id", util.UUIDToString(row.RuntimeID), "error", err)
		return
	}
	recipients := map[string]pgtype.UUID{}
	if exceeded.UserID.Valid {
		recipients[util.UUIDToString(exceeded.UserID)] = exceeded.UserID
	}
	if rt.OwnerID.Valid {
		recipients[util.UUIDToString(rt.OwnerID)] = rt.OwnerID
	}
	var userID *string
	if exceeded.UserID.Valid {
		v := util.UUIDToString(exceeded.UserID)
		userID = &v
	}
	details, err := json.Marshal(map[string]any{
		"scope": exceeded.Scope, "period": exceeded.Period,
		"runtime_id": util.UUIDToString(row.RuntimeID), "user_id": userID,
		"used_usd": pricing.TicksToUSD(exceeded.UsedTicks), "limit_usd": pricing.TicksToUSD(exceeded.LimitTicks),
		"period_start": exceeded.PeriodStart.UTC().Format(time.RFC3339), "reset_at": exceeded.ResetAt.UTC().Format(time.RFC3339),
	})
	if err != nil {
		slog.Warn("runtime budget notice: marshal details failed", "error", err)
		return
	}
	scopeLabel := "This runtime"
	if exceeded.Scope == RuntimeBudgetScopeUser {
		scopeLabel = "Your agents on this runtime"
	}
	body := fmt.Sprintf("%s reached the %s cost limit of $%.2f (used $%.2f). New runs are refused until %s UTC.",
		scopeLabel, exceeded.Period, pricing.TicksToUSD(exceeded.LimitTicks), pricing.TicksToUSD(exceeded.UsedTicks),
		exceeded.ResetAt.UTC().Format("Jan 2, 15:04"))
	for _, recipient := range recipients {
		item, err := q.CreateInboxItem(ctx, db.CreateInboxItemParams{
			ID: dbid.NewV7(), WorkspaceID: row.WorkspaceID,
			RecipientType: "member", RecipientID: recipient,
			Type: "runtime_budget_exceeded", Severity: "attention", IssueID: pgtype.UUID{},
			Title: "Runtime cost budget reached", Body: pgtype.Text{String: body, Valid: true},
			ActorType: pgtype.Text{String: "system", Valid: true}, ActorID: pgtype.UUID{},
			Details: details,
		})
		if err != nil {
			slog.Warn("runtime budget notice: create inbox item failed", "error", err)
			continue
		}
		if s.Bus != nil {
			s.Bus.Publish(events.Event{
				Type: protocol.EventInboxNew, WorkspaceID: util.UUIDToString(item.WorkspaceID), ActorType: "system",
				Payload: map[string]any{"item": map[string]any{
					"id": util.UUIDToString(item.ID), "workspace_id": util.UUIDToString(item.WorkspaceID),
					"recipient_type": item.RecipientType, "recipient_id": util.UUIDToString(item.RecipientID),
					"type": item.Type, "severity": item.Severity, "issue_id": nil,
					"issue_status": nil, "issue_priority": nil,
					"title": item.Title, "body": util.TextToPtr(item.Body), "read": item.Read,
					"archived": item.Archived, "created_at": util.TimestampToString(item.CreatedAt),
					"actor_type": util.TextToPtr(item.ActorType), "actor_id": nil,
					"details": json.RawMessage(item.Details),
				}},
			})
		}
	}
}
```

- [ ] **Step 4: Run the tests**

Run: `cd server && go test ./internal/service -run 'RuntimeCostBudget|RuntimeBudget' -count=1 && go vet ./internal/service`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add server/internal/service/runtime_cost_budget.go server/internal/service/runtime_cost_budget_test.go
git commit -m "feat(budget): notify the blocked user and runtime owner once per period"
```

---

### Task 5: Budget API (`GET`/`PUT`) and row cleanup

**Files:**
- Modify: `server/internal/service/runtime_cost_budget.go` (status computation for the API)
- Create: `server/internal/handler/runtime_cost_budget.go`, `server/internal/handler/runtime_cost_budget_test.go`
- Modify: `server/cmd/server/router.go` (inside `r.Route("/{runtimeId}", ...)` under `/api/runtimes`)
- Modify: `server/internal/handler/runtime.go:1004` and `:1216`, `server/internal/handler/daemon.go:874`, `server/internal/handler/workspace_revoke.go:223`

**Interfaces:**
- Produces in `service`:

```go
type RuntimeBudgetPeriodStatus struct {
	LimitTicks  int64
	UsedTicks   int64
	PeriodStart time.Time
	ResetAt     time.Time
	Reached     bool
}
type RuntimeBudgetScopeStatus struct {
	UserID  pgtype.UUID // invalid for the runtime total
	Periods map[pricing.Period]*RuntimeBudgetPeriodStatus // nil entry = unlimited
}
type RuntimeBudgetStatus struct {
	Runtime *RuntimeBudgetScopeStatus // nil when no total row
	Users   []RuntimeBudgetScopeStatus
}
func (s *TaskService) RuntimeCostBudgetStatus(ctx context.Context, runtimeID pgtype.UUID, now time.Time) (RuntimeBudgetStatus, error)
```

- Produces HTTP `GET /api/runtimes/{runtimeId}/budget` and `PUT /api/runtimes/{runtimeId}/budget` with the JSON shapes from the spec:

```json
GET/PUT response: {"runtime": {"daily": {...}|null, "weekly": ..., "monthly": ...}|null,
                   "users": [{"user_id": "...", "daily": ..., "weekly": ..., "monthly": ...}],
                   "can_manage": true}
period object:    {"limit_usd": 20, "used_usd": 3.42, "period_start": "...", "reset_at": "...", "reached": false}
PUT body:         {"runtime": {"daily_usd": 20|null, "weekly_usd": null, "monthly_usd": null}|null,
                   "users": [{"user_id": "...", "daily_usd": null, "weekly_usd": 50, "monthly_usd": null}]}
```

- [ ] **Step 1: Add the status computation to the service**

Append to `server/internal/service/runtime_cost_budget.go`:

```go
// RuntimeBudgetPeriodStatus is one configured period of one scope.
type RuntimeBudgetPeriodStatus struct {
	LimitTicks  int64
	UsedTicks   int64
	PeriodStart time.Time
	ResetAt     time.Time
	Reached     bool
}

// RuntimeBudgetScopeStatus is the runtime total (UserID invalid) or one user.
type RuntimeBudgetScopeStatus struct {
	UserID  pgtype.UUID
	Periods map[pricing.Period]*RuntimeBudgetPeriodStatus
}

// RuntimeBudgetStatus is the read model behind GET /api/runtimes/{id}/budget.
type RuntimeBudgetStatus struct {
	Runtime *RuntimeBudgetScopeStatus
	Users   []RuntimeBudgetScopeStatus
}

func scopeStatus(ctx context.Context, q *db.Queries, row db.RuntimeCostBudget, now time.Time) (RuntimeBudgetScopeStatus, error) {
	out := RuntimeBudgetScopeStatus{UserID: row.UserID, Periods: map[pricing.Period]*RuntimeBudgetPeriodStatus{}}
	for _, p := range pricing.AllPeriods {
		limit, ok := budgetLimit(row, p)
		if !ok {
			out.Periods[p] = nil
			continue
		}
		start := pricing.PeriodStart(now, p)
		used, err := runtimeSpendTicks(ctx, q, row.RuntimeID, row.UserID, start)
		if err != nil {
			return out, err
		}
		out.Periods[p] = &RuntimeBudgetPeriodStatus{
			LimitTicks: limit, UsedTicks: used, PeriodStart: start,
			ResetAt: pricing.NextPeriodStart(now, p), Reached: used >= limit,
		}
	}
	return out, nil
}

// RuntimeCostBudgetStatus loads every budget row of a runtime with its
// current-period spend. Spend is computed on demand, never stored.
func (s *TaskService) RuntimeCostBudgetStatus(ctx context.Context, runtimeID pgtype.UUID, now time.Time) (RuntimeBudgetStatus, error) {
	rows, err := s.Queries.ListRuntimeCostBudgets(ctx, runtimeID)
	if err != nil {
		return RuntimeBudgetStatus{}, fmt.Errorf("list runtime cost budgets: %w", err)
	}
	status := RuntimeBudgetStatus{Users: []RuntimeBudgetScopeStatus{}}
	for _, row := range rows {
		sc, err := scopeStatus(ctx, s.Queries, row, now)
		if err != nil {
			return RuntimeBudgetStatus{}, err
		}
		if row.UserID.Valid {
			status.Users = append(status.Users, sc)
		} else {
			copy := sc
			status.Runtime = &copy
		}
	}
	return status, nil
}
```

- [ ] **Step 2: Write the failing handler tests**

`server/internal/handler/runtime_cost_budget_test.go`:

```go
package handler

import (
	"net/http"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
)

func budgetRequest(t *testing.T, userID, method, runtimeID string, body any) *http.Request {
	t.Helper()
	req := newRequest(method, "/api/runtimes/"+runtimeID+"/budget", body)
	req.Header.Set("X-User-ID", userID)
	return withURLParam(req, "runtimeId", runtimeID)
}

func TestPutRuntimeCostBudgetRequiresOwnerOrAdmin(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	runtimeID := dbfx.Runtime(t, "budget-rt", testutil.Cols{"visibility": "public"})
	memberUserID := dbfx.User(t, "Budget Member", "budget-member@example.com")
	dbfx.Member(t, testWorkspaceID, memberUserID, "member")
	adminUserID := dbfx.User(t, "Budget Admin", "budget-admin@example.com")
	dbfx.Member(t, testWorkspaceID, adminUserID, "admin")
	body := map[string]any{"runtime": map[string]any{"daily_usd": 20}, "users": []any{}}

	testutil.Call(t, testHandler.PutRuntimeCostBudget, budgetRequest(t, memberUserID, http.MethodPut, runtimeID, body)).Want(http.StatusForbidden)
	testutil.Call(t, testHandler.PutRuntimeCostBudget, budgetRequest(t, adminUserID, http.MethodPut, runtimeID, body)).Want(http.StatusOK)
	// testUserID is the workspace owner.
	var out map[string]any
	testutil.Call(t, testHandler.PutRuntimeCostBudget, budgetRequest(t, testUserID, http.MethodPut, runtimeID, body)).Want(http.StatusOK).JSON(&out)
	rt := out["runtime"].(map[string]any)
	if rt["daily"].(map[string]any)["limit_usd"].(float64) != 20 {
		t.Fatalf("runtime.daily = %#v", rt["daily"])
	}
	if rt["weekly"] != nil || rt["monthly"] != nil {
		t.Fatalf("unset periods must be null, got %#v", rt)
	}
	if out["can_manage"] != true {
		t.Fatalf("can_manage = %#v", out["can_manage"])
	}
	t.Cleanup(func() { dbfx.Exec(t, `DELETE FROM runtime_cost_budget WHERE runtime_id = $1`, runtimeID) })
}

func TestPutRuntimeCostBudgetValidatesAmountsAndMembers(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	runtimeID := dbfx.Runtime(t, "budget-rt-validate", testutil.Cols{"visibility": "public"})
	cases := []struct {
		name string
		body map[string]any
	}{
		{"zero", map[string]any{"runtime": map[string]any{"daily_usd": 0}}},
		{"negative", map[string]any{"runtime": map[string]any{"daily_usd": -1}}},
		{"too large", map[string]any{"runtime": map[string]any{"daily_usd": 1000001}}},
		{"three decimals", map[string]any{"runtime": map[string]any{"daily_usd": 1.005}}},
		{"non member", map[string]any{"users": []any{map[string]any{"user_id": "00000000-0000-0000-0000-000000000001", "daily_usd": 1}}}},
		{"duplicate user", map[string]any{"users": []any{
			map[string]any{"user_id": testUserID, "daily_usd": 1},
			map[string]any{"user_id": testUserID, "weekly_usd": 1},
		}}},
	}
	for _, tc := range cases {
		testutil.Call(t, testHandler.PutRuntimeCostBudget, budgetRequest(t, testUserID, http.MethodPut, runtimeID, tc.body)).Want(http.StatusBadRequest)
	}
}

func TestPutRuntimeCostBudgetReplacesAndDropsEmptiedRows(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	runtimeID := dbfx.Runtime(t, "budget-rt-replace", testutil.Cols{"visibility": "public"})
	t.Cleanup(func() { dbfx.Exec(t, `DELETE FROM runtime_cost_budget WHERE runtime_id = $1`, runtimeID) })
	first := map[string]any{
		"runtime": map[string]any{"daily_usd": 20, "weekly_usd": 300},
		"users":   []any{map[string]any{"user_id": testUserID, "monthly_usd": 200}},
	}
	testutil.Call(t, testHandler.PutRuntimeCostBudget, budgetRequest(t, testUserID, http.MethodPut, runtimeID, first)).Want(http.StatusOK)
	if n := dbfx.Count(t, `SELECT count(*) FROM runtime_cost_budget WHERE runtime_id = $1`, runtimeID); n != 2 {
		t.Fatalf("rows after first put = %d, want 2", n)
	}
	// Runtime total emptied and the user row dropped from the list: both go away.
	second := map[string]any{"runtime": map[string]any{}, "users": []any{}}
	var out map[string]any
	testutil.Call(t, testHandler.PutRuntimeCostBudget, budgetRequest(t, testUserID, http.MethodPut, runtimeID, second)).Want(http.StatusOK).JSON(&out)
	if n := dbfx.Count(t, `SELECT count(*) FROM runtime_cost_budget WHERE runtime_id = $1`, runtimeID); n != 0 {
		t.Fatalf("rows after empty put = %d, want 0", n)
	}
	if out["runtime"] != nil || len(out["users"].([]any)) != 0 {
		t.Fatalf("empty state = %#v", out)
	}
}

func TestGetRuntimeCostBudgetReportsUsedAndReached(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	runtimeID := dbfx.Runtime(t, "budget-rt-get", testutil.Cols{"visibility": "public"})
	agentID := dbfx.Agent(t, "budget-agent", runtimeID, testutil.Cols{"owner_id": testUserID})
	taskID := dbfx.Task(t, agentID, testutil.Cols{"runtime_id": runtimeID, "status": "completed"})
	dbfx.Insert(t, "task_usage", testutil.Cols{
		"task_id": taskID, "provider": "xai", "model": "grok-4.5",
		"input_tokens": int64(0), "output_tokens": int64(0), "cache_read_tokens": int64(0), "cache_write_tokens": int64(0),
		"cost_usd_ticks": int64(250_000_000_000), // $25
	})
	t.Cleanup(func() { dbfx.Exec(t, `DELETE FROM runtime_cost_budget WHERE runtime_id = $1`, runtimeID) })
	body := map[string]any{
		"runtime": map[string]any{"monthly_usd": 100},
		"users":   []any{map[string]any{"user_id": testUserID, "daily_usd": 20}},
	}
	testutil.Call(t, testHandler.PutRuntimeCostBudget, budgetRequest(t, testUserID, http.MethodPut, runtimeID, body)).Want(http.StatusOK)

	var out map[string]any
	testutil.Call(t, testHandler.GetRuntimeCostBudget, budgetRequest(t, testUserID, http.MethodGet, runtimeID, nil)).Want(http.StatusOK).JSON(&out)
	monthly := out["runtime"].(map[string]any)["monthly"].(map[string]any)
	if monthly["used_usd"].(float64) != 25 || monthly["reached"] != false {
		t.Fatalf("runtime.monthly = %#v", monthly)
	}
	users := out["users"].([]any)
	daily := users[0].(map[string]any)["daily"].(map[string]any)
	if daily["used_usd"].(float64) != 25 || daily["reached"] != true {
		t.Fatalf("users[0].daily = %#v", daily)
	}
	if daily["reset_at"] == "" || daily["period_start"] == "" {
		t.Fatalf("period timestamps missing: %#v", daily)
	}
}

func TestGetRuntimeCostBudgetMemberSeesCanManageFalse(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	runtimeID := dbfx.Runtime(t, "budget-rt-member", testutil.Cols{"visibility": "public"})
	memberUserID := dbfx.User(t, "Budget Viewer", "budget-viewer@example.com")
	dbfx.Member(t, testWorkspaceID, memberUserID, "member")
	var out map[string]any
	testutil.Call(t, testHandler.GetRuntimeCostBudget, budgetRequest(t, memberUserID, http.MethodGet, runtimeID, nil)).Want(http.StatusOK).JSON(&out)
	if out["can_manage"] != false {
		t.Fatalf("can_manage = %#v, want false", out["can_manage"])
	}
}
```

If `dbfx.Task` does not set `created_at` for the usage row, the default `now()` on `task_usage.created_at` places it in the current UTC day, week and month, which is what the assertions need.

- [ ] **Step 3: Run to verify failure**

Run: `cd server && go test ./internal/handler -run 'RuntimeCostBudget' -count=1`
Expected: FAIL to compile, `testHandler.PutRuntimeCostBudget undefined`.

- [ ] **Step 4: Implement the handler**

`server/internal/handler/runtime_cost_budget.go`:

```go
package handler

import (
	"encoding/json"
	"math"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/dbid"
	obsmetrics "github.com/multica-ai/multica/server/internal/metrics"
	"github.com/multica-ai/multica/server/internal/pricing"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Wire shapes for /api/runtimes/{id}/budget. Amounts are USD numbers; the
// database keeps ticks. A null period means unlimited.
type RuntimeBudgetPeriodResponse struct {
	LimitUSD    float64 `json:"limit_usd"`
	UsedUSD     float64 `json:"used_usd"`
	PeriodStart string  `json:"period_start"`
	ResetAt     string  `json:"reset_at"`
	Reached     bool    `json:"reached"`
}

type RuntimeBudgetScopeResponse struct {
	UserID  string                       `json:"user_id,omitempty"`
	Daily   *RuntimeBudgetPeriodResponse `json:"daily"`
	Weekly  *RuntimeBudgetPeriodResponse `json:"weekly"`
	Monthly *RuntimeBudgetPeriodResponse `json:"monthly"`
}

type RuntimeBudgetResponse struct {
	Runtime   *RuntimeBudgetScopeResponse  `json:"runtime"`
	Users     []RuntimeBudgetScopeResponse `json:"users"`
	CanManage bool                         `json:"can_manage"`
}

type runtimeBudgetScopeInput struct {
	UserID     string   `json:"user_id"`
	DailyUSD   *float64 `json:"daily_usd"`
	WeeklyUSD  *float64 `json:"weekly_usd"`
	MonthlyUSD *float64 `json:"monthly_usd"`
}

type runtimeBudgetPutRequest struct {
	Runtime *runtimeBudgetScopeInput  `json:"runtime"`
	Users   []runtimeBudgetScopeInput `json:"users"`
}

const maxBudgetUSD = 1_000_000

// The runtime-total scope key in DeleteRuntimeCostBudgetsExcept.
var zeroUUID = pgtype.UUID{Valid: true}

func canManageRuntimeBudget(member db.Member) bool {
	return roleAllowed(member.Role, "owner", "admin")
}

func periodResponse(p *service.RuntimeBudgetPeriodStatus) *RuntimeBudgetPeriodResponse {
	if p == nil {
		return nil
	}
	return &RuntimeBudgetPeriodResponse{
		LimitUSD:    pricing.TicksToUSD(p.LimitTicks),
		UsedUSD:     pricing.TicksToUSD(p.UsedTicks),
		PeriodStart: p.PeriodStart.UTC().Format(time.RFC3339),
		ResetAt:     p.ResetAt.UTC().Format(time.RFC3339),
		Reached:     p.Reached,
	}
}

func scopeResponse(sc service.RuntimeBudgetScopeStatus) RuntimeBudgetScopeResponse {
	out := RuntimeBudgetScopeResponse{
		Daily:   periodResponse(sc.Periods[pricing.PeriodDaily]),
		Weekly:  periodResponse(sc.Periods[pricing.PeriodWeekly]),
		Monthly: periodResponse(sc.Periods[pricing.PeriodMonthly]),
	}
	if sc.UserID.Valid {
		out.UserID = uuidToString(sc.UserID)
	}
	return out
}

func (h *Handler) writeRuntimeBudget(w http.ResponseWriter, r *http.Request, runtimeID pgtype.UUID, member db.Member) {
	status, err := h.TaskService.RuntimeCostBudgetStatus(r.Context(), runtimeID, time.Now())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load runtime budget")
		return
	}
	resp := RuntimeBudgetResponse{Users: []RuntimeBudgetScopeResponse{}, CanManage: canManageRuntimeBudget(member)}
	if status.Runtime != nil {
		sc := scopeResponse(*status.Runtime)
		resp.Runtime = &sc
	}
	for _, u := range status.Users {
		resp.Users = append(resp.Users, scopeResponse(u))
	}
	writeJSON(w, http.StatusOK, resp)
}

// GetRuntimeCostBudget: anyone who can read the runtime sees every scope's
// limit and spend, the same visibility the Cost-by-owner tab already has.
func (h *Handler) GetRuntimeCostBudget(w http.ResponseWriter, r *http.Request) {
	rt, member, ok := h.requireRuntimeReadAccess(w, r, obsmetrics.RuntimeLookupSourceRuntimeAPI, chi.URLParam(r, "runtimeId"))
	if !ok {
		return
	}
	h.writeRuntimeBudget(w, r, rt.ID, member)
}

// validBudgetAmount accepts a positive finite USD amount with at most two
// decimals and returns its ticks. nil means "no limit".
func validBudgetAmount(v *float64) (pgtype.Int8, bool) {
	if v == nil {
		return pgtype.Int8{}, true
	}
	usd := *v
	if math.IsNaN(usd) || math.IsInf(usd, 0) || usd <= 0 || usd > maxBudgetUSD {
		return pgtype.Int8{}, false
	}
	cents := usd * 100
	if math.Abs(cents-math.Round(cents)) > 1e-6 {
		return pgtype.Int8{}, false
	}
	return pgtype.Int8{Int64: pricing.USDToTicks(usd), Valid: true}, true
}

// PutRuntimeCostBudget replaces the whole budget set of a runtime. Owner and
// admin only; the runtime owner has no say because budgets are workspace
// governance, not machine access.
func (h *Handler) PutRuntimeCostBudget(w http.ResponseWriter, r *http.Request) {
	runtimeID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "runtimeId"), "runtime_id")
	if !ok {
		return
	}
	rt, err := h.getAgentRuntime(r.Context(), obsmetrics.RuntimeLookupSourceRuntimeAPI, runtimeID)
	if err != nil {
		writeError(w, http.StatusNotFound, "runtime not found")
		return
	}
	member, ok := h.requireWorkspaceRole(w, r, uuidToString(rt.WorkspaceID), "runtime not found", "owner", "admin")
	if !ok {
		return
	}
	var req runtimeBudgetPutRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	type upsert struct {
		userID  pgtype.UUID
		daily   pgtype.Int8
		weekly  pgtype.Int8
		monthly pgtype.Int8
	}
	var writes []upsert
	keep := []pgtype.UUID{}
	parseScope := func(in runtimeBudgetScopeInput, userID pgtype.UUID) bool {
		daily, ok1 := validBudgetAmount(in.DailyUSD)
		weekly, ok2 := validBudgetAmount(in.WeeklyUSD)
		monthly, ok3 := validBudgetAmount(in.MonthlyUSD)
		if !ok1 || !ok2 || !ok3 {
			writeError(w, http.StatusBadRequest, "budget amounts must be positive USD with at most two decimals, up to 1,000,000")
			return false
		}
		if !daily.Valid && !weekly.Valid && !monthly.Valid {
			return true // all empty: the scope is removed, nothing to keep
		}
		writes = append(writes, upsert{userID: userID, daily: daily, weekly: weekly, monthly: monthly})
		if userID.Valid {
			keep = append(keep, userID)
		} else {
			keep = append(keep, zeroUUID)
		}
		return true
	}
	if req.Runtime != nil && !parseScope(*req.Runtime, pgtype.UUID{}) {
		return
	}
	seen := map[string]bool{}
	for _, u := range req.Users {
		userID, ok := parseUUIDOrBadRequest(w, u.UserID, "user_id")
		if !ok {
			return
		}
		if seen[u.UserID] {
			writeError(w, http.StatusBadRequest, "user_id listed more than once")
			return
		}
		seen[u.UserID] = true
		if _, err := h.getWorkspaceMember(r.Context(), u.UserID, uuidToString(rt.WorkspaceID)); err != nil {
			writeError(w, http.StatusBadRequest, "user_id is not a workspace member")
			return
		}
		if !parseScope(u, userID) {
			return
		}
	}

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save runtime budget")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)
	for _, wr := range writes {
		if _, err := qtx.UpsertRuntimeCostBudget(r.Context(), db.UpsertRuntimeCostBudgetParams{
			ID: dbid.NewV7(), WorkspaceID: rt.WorkspaceID, RuntimeID: rt.ID, UserID: wr.userID,
			DailyLimitUsdTicks: wr.daily, WeeklyLimitUsdTicks: wr.weekly, MonthlyLimitUsdTicks: wr.monthly,
			UpdatedBy: member.UserID,
		}); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to save runtime budget")
			return
		}
	}
	if err := qtx.DeleteRuntimeCostBudgetsExcept(r.Context(), db.DeleteRuntimeCostBudgetsExceptParams{
		RuntimeID: rt.ID, KeepKeys: keep,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save runtime budget")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save runtime budget")
		return
	}
	h.writeRuntimeBudget(w, r, rt.ID, member)
}
```

`h.TxStarter.Begin(r.Context())` and `h.Queries.WithTx(tx)` are the same calls `DeleteAgentRuntime` in `runtime.go:946-952` uses. If the `UpsertRuntimeCostBudgetParams` positional field for `$4` is named differently from `UpdatedBy`, use the generated name. If `zeroUUID` needs explicit zero bytes, `pgtype.UUID{Bytes: [16]byte{}, Valid: true}` is the same value.

- [ ] **Step 5: Register the routes**

In `server/cmd/server/router.go`, inside `r.Route("/{runtimeId}", func(r chi.Router) {` under `/api/runtimes`, after the `usage/by-hour` line:

```go
					r.Get("/budget", h.GetRuntimeCostBudget)
					r.Put("/budget", h.PutRuntimeCostBudget)
```

- [ ] **Step 6: Run the handler tests**

Run: `cd server && go test ./internal/handler -run 'RuntimeCostBudget' -count=1`
Expected: PASS.

- [ ] **Step 7: Delete budget rows with their runtime or member**

In `server/internal/handler/runtime.go`, immediately before each `qtx.DeleteAgentRuntime(r.Context(), rt.ID)` (lines ~1004 and ~1216):

```go
	if err := qtx.DeleteRuntimeCostBudgetsForRuntime(r.Context(), rt.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete runtime")
		return
	}
```

In `server/internal/handler/daemon.go` before `qtx.DeleteAgentRuntime(ctx, oldRuntimeID)` (~line 874):

```go
	if err := qtx.DeleteRuntimeCostBudgetsForRuntime(ctx, oldRuntimeID); err != nil {
		return fmt.Errorf("delete old runtime budgets: %w", err)
	}
```

In `server/internal/handler/workspace_revoke.go` before `qtx.DeleteMember(ctx, memberID)` (~line 223), where `workspaceID` and `userID` are already in scope:

```go
	if err := qtx.DeleteRuntimeCostBudgetsForWorkspaceUser(ctx, db.DeleteRuntimeCostBudgetsForWorkspaceUserParams{
		WorkspaceID: workspaceID,
		UserID:      userID,
	}); err != nil {
		return empty, err
	}
```

- [ ] **Step 8: Add a cleanup assertion to the delete test**

Append to `server/internal/handler/runtime_cost_budget_test.go`:

```go
func TestDeleteRuntimeRemovesItsBudgets(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	runtimeID := dbfx.Runtime(t, "budget-rt-delete", testutil.Cols{"visibility": "public", "owner_id": testUserID})
	body := map[string]any{"runtime": map[string]any{"daily_usd": 1}}
	testutil.Call(t, testHandler.PutRuntimeCostBudget, budgetRequest(t, testUserID, http.MethodPut, runtimeID, body)).Want(http.StatusOK)
	req := withURLParam(newRequest(http.MethodDelete, "/api/runtimes/"+runtimeID, nil), "runtimeId", runtimeID)
	testutil.Call(t, testHandler.DeleteAgentRuntime, req).WantOneOf(http.StatusOK, http.StatusNoContent)
	if n := dbfx.Count(t, `SELECT count(*) FROM runtime_cost_budget WHERE runtime_id = $1`, runtimeID); n != 0 {
		t.Fatalf("budget rows survived runtime delete: %d", n)
	}
}
```

Run: `cd server && go test ./internal/handler -run 'RuntimeCostBudget|DeleteRuntimeRemovesItsBudgets' -count=1 && go vet ./internal/handler && go build ./...`
Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add server/internal/service/runtime_cost_budget.go server/internal/handler/runtime_cost_budget.go server/internal/handler/runtime_cost_budget_test.go \
        server/cmd/server/router.go server/internal/handler/runtime.go server/internal/handler/daemon.go server/internal/handler/workspace_revoke.go
git commit -m "feat(api): runtime cost budget read and replace endpoints"
```

---

### Task 6: `packages/core` types, schema, client, query, mutation, permission

**Files:**
- Modify: `packages/core/types/agent.ts` (after `RuntimeUsageByHour`)
- Modify: `packages/core/api/schemas.ts` (after `RuntimeUsageByHourListSchema`), `packages/core/api/schemas.test.ts`
- Modify: `packages/core/api/client.ts` (after `getRuntimeUsageByHour`; add the new type + schema to the two import lists at the top)
- Modify: `packages/core/runtimes/queries.ts`, `packages/core/runtimes/queries.test.ts`, `packages/core/runtimes/mutations.ts`
- Modify: `packages/core/permissions/rules.ts`, `rules.test.ts`, `index.ts`
- Modify: `packages/core/types/inbox.ts` (union)

**Interfaces:**
- Produces types:

```ts
export type RuntimeBudgetPeriodKey = "daily" | "weekly" | "monthly";
export interface RuntimeBudgetPeriod { limit_usd: number; used_usd: number; period_start: string; reset_at: string; reached: boolean; }
export interface RuntimeBudgetScope { user_id?: string; daily: RuntimeBudgetPeriod | null; weekly: RuntimeBudgetPeriod | null; monthly: RuntimeBudgetPeriod | null; }
export interface RuntimeCostBudget { runtime: RuntimeBudgetScope | null; users: RuntimeBudgetScope[]; can_manage: boolean; }
export interface RuntimeBudgetScopeInput { user_id?: string; daily_usd: number | null; weekly_usd: number | null; monthly_usd: number | null; }
export interface RuntimeCostBudgetInput { runtime: RuntimeBudgetScopeInput | null; users: RuntimeBudgetScopeInput[]; }
```
- Produces `RuntimeCostBudgetSchema`, `EMPTY_RUNTIME_COST_BUDGET`, `api.getRuntimeCostBudget(runtimeId)`, `api.updateRuntimeCostBudget(runtimeId, input)`, `runtimeKeys.budget(rid)`, `runtimeCostBudgetOptions(runtimeId)`, `useUpdateRuntimeCostBudget(wsId)`, `canManageRuntimeBudget(ctx)`.

- [ ] **Step 1: Add the types**

Append to `packages/core/types/agent.ts` after the `RuntimeUsageByHour` interface:

```ts
// Runtime cost budget (`/api/runtimes/:id/budget`). Periods are UTC calendar
// windows; a null period is unlimited. `runtime` is the total scope (blocks
// everyone), `users` are per-owner scopes. `can_manage` mirrors the server's
// owner/admin gate so the UI shows the editor only when a PUT would succeed.
export type RuntimeBudgetPeriodKey = "daily" | "weekly" | "monthly";

export interface RuntimeBudgetPeriod {
  limit_usd: number;
  used_usd: number;
  period_start: string;
  reset_at: string;
  reached: boolean;
}

export interface RuntimeBudgetScope {
  user_id?: string;
  daily: RuntimeBudgetPeriod | null;
  weekly: RuntimeBudgetPeriod | null;
  monthly: RuntimeBudgetPeriod | null;
}

export interface RuntimeCostBudget {
  runtime: RuntimeBudgetScope | null;
  users: RuntimeBudgetScope[];
  can_manage: boolean;
}

export interface RuntimeBudgetScopeInput {
  user_id?: string;
  daily_usd: number | null;
  weekly_usd: number | null;
  monthly_usd: number | null;
}

export interface RuntimeCostBudgetInput {
  runtime: RuntimeBudgetScopeInput | null;
  users: RuntimeBudgetScopeInput[];
}
```

Add `"runtime_budget_exceeded"` to the `InboxItem["type"]` union in `packages/core/types/inbox.ts` after `"autopilot_quota_exceeded"`.

- [ ] **Step 2: Write the failing schema test**

In `packages/core/api/schemas.test.ts`, import `RuntimeCostBudgetSchema` and `EMPTY_RUNTIME_COST_BUDGET` from `./schemas`, and add near the other runtime usage tests:

```ts
  it("parses a runtime cost budget and defaults missing scopes", () => {
    const full = RuntimeCostBudgetSchema.parse({
      runtime: { daily: { limit_usd: 20, used_usd: 3.42, period_start: "a", reset_at: "b", reached: false }, weekly: null, monthly: null },
      users: [{ user_id: "u1", daily: null, weekly: { limit_usd: 50, used_usd: 51, period_start: "a", reset_at: "b", reached: true }, monthly: null }],
      can_manage: true,
    });
    expect(full.runtime?.daily?.limit_usd).toBe(20);
    expect(full.users[0]?.weekly?.reached).toBe(true);

    const sparse = RuntimeCostBudgetSchema.parse({});
    expect(sparse).toEqual({ runtime: null, users: [], can_manage: false });

    const partialPeriod = RuntimeCostBudgetSchema.parse({ runtime: { daily: { limit_usd: 5 } } });
    expect(partialPeriod.runtime?.daily).toEqual({ limit_usd: 5, used_usd: 0, period_start: "", reset_at: "", reached: false });
    expect(partialPeriod.runtime?.weekly).toBeNull();
  });

  it("rejects a runtime cost budget body that is not an object", () => {
    expect(RuntimeCostBudgetSchema.safeParse([]).success).toBe(false);
    expect(EMPTY_RUNTIME_COST_BUDGET).toEqual({ runtime: null, users: [], can_manage: false });
  });
```

Run: `pnpm --filter @multica/core exec vitest run api/schemas.test.ts -t "runtime cost budget"`
Expected: FAIL, export not found.

- [ ] **Step 3: Add the schema**

In `packages/core/api/schemas.ts` after `RuntimeUsageByHourListSchema`:

```ts
const RuntimeBudgetPeriodSchema = z.object({
  limit_usd: z.number().default(0),
  used_usd: z.number().default(0),
  period_start: z.string().default(""),
  reset_at: z.string().default(""),
  reached: z.boolean().default(false),
}).loose();

const RuntimeBudgetScopeSchema = z.object({
  user_id: z.string().optional(),
  daily: RuntimeBudgetPeriodSchema.nullable().default(null),
  weekly: RuntimeBudgetPeriodSchema.nullable().default(null),
  monthly: RuntimeBudgetPeriodSchema.nullable().default(null),
}).loose();

export const RuntimeCostBudgetSchema = z.object({
  runtime: RuntimeBudgetScopeSchema.nullable().default(null),
  users: z.array(RuntimeBudgetScopeSchema).default([]),
  can_manage: z.boolean().default(false),
}).loose();

export const EMPTY_RUNTIME_COST_BUDGET: RuntimeCostBudget = {
  runtime: null,
  users: [],
  can_manage: false,
};
```

Add `RuntimeCostBudget` to the type import list at the top of `schemas.ts`. Run the test again: PASS.

- [ ] **Step 4: Add the client methods**

In `packages/core/api/client.ts` add `RuntimeCostBudget`, `RuntimeCostBudgetInput` to the types import and `RuntimeCostBudgetSchema`, `EMPTY_RUNTIME_COST_BUDGET` to the schemas import, then after `getRuntimeUsageByHour`:

```ts
  async getRuntimeCostBudget(runtimeId: string): Promise<RuntimeCostBudget> {
    const raw = await this.fetch<unknown>(`/api/runtimes/${runtimeId}/budget`);
    return parseWithFallback<RuntimeCostBudget>(raw, RuntimeCostBudgetSchema, EMPTY_RUNTIME_COST_BUDGET, {
      endpoint: "GET /api/runtimes/:id/budget",
    });
  }

  // Full replace: scopes missing from `input` are removed server-side.
  async updateRuntimeCostBudget(
    runtimeId: string,
    input: RuntimeCostBudgetInput,
  ): Promise<RuntimeCostBudget> {
    const raw = await this.fetch<unknown>(`/api/runtimes/${runtimeId}/budget`, {
      method: "PUT",
      body: JSON.stringify(input),
    });
    return parseWithFallback<RuntimeCostBudget>(raw, RuntimeCostBudgetSchema, EMPTY_RUNTIME_COST_BUDGET, {
      endpoint: "PUT /api/runtimes/:id/budget",
    });
  }
```

Match the `body`/`headers` convention of the neighbouring `updateRuntime` method exactly (if it passes a plain object and lets `fetch` serialize, do the same).

- [ ] **Step 5: Query key, options and mutation**

`packages/core/runtimes/queries.ts`: add to `runtimeKeys`:

```ts
  budget: (rid: string) => ["runtimes", "budget", rid] as const,
```

and after `runtimeUsageByHourOptions`:

```ts
export function runtimeCostBudgetOptions(runtimeId: string) {
  return queryOptions({
    queryKey: runtimeKeys.budget(runtimeId),
    queryFn: () => api.getRuntimeCostBudget(runtimeId),
    staleTime: 30 * 1000,
  });
}
```

`packages/core/runtimes/queries.test.ts`: add

```ts
  it("keys the budget by runtime id only (periods are UTC, no tz)", () => {
    expect(runtimeKeys.budget("rt-1")).toEqual(["runtimes", "budget", "rt-1"]);
  });
```

(import `runtimeKeys` if the file does not already).

`packages/core/runtimes/mutations.ts`: append

```ts
// Replaces the runtime's whole budget set. Invalidates the budget query so
// meters reflect the new limits, and the runtime list in case a summary
// column reads budgets later.
export function useUpdateRuntimeCostBudget(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ runtimeId, input }: { runtimeId: string; input: RuntimeCostBudgetInput }) =>
      api.updateRuntimeCostBudget(runtimeId, input),
    onSettled: (_data, _err, { runtimeId }) => {
      qc.invalidateQueries({ queryKey: runtimeKeys.budget(runtimeId) });
      qc.invalidateQueries({ queryKey: runtimeKeys.all(wsId) });
    },
  });
}
```

with `import type { RuntimeCostBudgetInput } from "../types";`.

- [ ] **Step 6: Permission rule**

`packages/core/permissions/rules.ts`, in the Runtimes section:

```ts
/**
 * Set or change a runtime's cost budget. Mirrors `PutRuntimeCostBudget`
 * (`server/internal/handler/runtime_cost_budget.go`): workspace owner or
 * admin only. The runtime owner has no override — budgets are governance.
 */
export function canManageRuntimeBudget(ctx: PermissionContext): Decision {
  if (isAdminLike(ctx.role)) return ALLOW;
  return deny(
    "not_admin_role",
    "Only workspace owners and admins can set runtime cost budgets.",
  );
}
```

`packages/core/permissions/rules.test.ts`: add

```ts
describe("canManageRuntimeBudget", () => {
  it("allows owner and admin, denies member and signed-out", () => {
    expect(canManageRuntimeBudget({ userId: ALICE, role: "owner" }).allowed).toBe(true);
    expect(canManageRuntimeBudget({ userId: ALICE, role: "admin" }).allowed).toBe(true);
    expect(canManageRuntimeBudget({ userId: ALICE, role: "member" }).allowed).toBe(false);
    expect(canManageRuntimeBudget({ userId: null, role: null }).allowed).toBe(false);
  });
});
```

(shape the `PermissionContext` literal like the other tests in the file; check `./types` for its exact fields). Export `canManageRuntimeBudget` from `packages/core/permissions/index.ts` next to `canEditAgent`.

- [ ] **Step 7: Typecheck and test core**

Run: `pnpm --filter @multica/core typecheck && pnpm --filter @multica/core test`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add packages/core
git commit -m "feat(core): runtime cost budget types, schema, client, query and permission"
```

---

### Task 7: `BudgetSection` and `RuntimeBudgetDialog` in `packages/views`

**Files:**
- Create: `packages/views/runtimes/budget.ts`, `packages/views/runtimes/budget.test.ts`
- Create: `packages/views/runtimes/components/budget-section.tsx`, `budget-section.test.tsx`
- Create: `packages/views/runtimes/components/runtime-budget-dialog.tsx`, `runtime-budget-dialog.test.tsx`
- Modify: `packages/views/runtimes/components/runtime-detail.tsx:196-199` (mount between `HeroCard` and `UsageSection`)
- Modify: `packages/views/locales/{en,zh-Hans,ja,ko}/runtimes.json` (new `budget` namespace)

**Interfaces:**
- Consumes `runtimeCostBudgetOptions`, `useUpdateRuntimeCostBudget`, `canManageRuntimeBudget`, `useCurrentMember`, `memberListOptions`, `formatUsd` from `../utils`, `Button`, `Dialog*`, `Input`, `Label`, `ActorAvatar`, `useT("runtimes")`.
- Produces pure helpers in `budget.ts`:

```ts
export function budgetPercent(p: RuntimeBudgetPeriod | null): number;      // 0..100, 100 when reached
export function countReachedUsers(users: RuntimeBudgetScope[]): number;     // users with any reached period
export function budgetToInput(b: RuntimeCostBudget): RuntimeCostBudgetInput;
export function parseBudgetField(raw: string): number | null | undefined;   // "" -> null, invalid -> undefined
export function scopeIsEmpty(s: RuntimeBudgetScopeInput): boolean;
```
- Produces `<BudgetSection runtime={runtime} />` and `<RuntimeBudgetDialog open onOpenChange runtimeId budget members />`.

- [ ] **Step 1: Write the failing helper tests**

`packages/views/runtimes/budget.test.ts`:

```ts
// @vitest-environment node
import { describe, expect, it } from "vitest";
import {
  budgetPercent,
  budgetToInput,
  countReachedUsers,
  parseBudgetField,
  scopeIsEmpty,
} from "./budget";

const period = (limit: number, used: number, reached = used >= limit) => ({
  limit_usd: limit,
  used_usd: used,
  period_start: "2026-09-02T00:00:00Z",
  reset_at: "2026-09-03T00:00:00Z",
  reached,
});

describe("budgetPercent", () => {
  it("is 0 for an unlimited period and clamps to 100", () => {
    expect(budgetPercent(null)).toBe(0);
    expect(budgetPercent(period(20, 3.42))).toBeCloseTo(17.1);
    expect(budgetPercent(period(20, 20.31))).toBe(100);
    expect(budgetPercent(period(0, 5))).toBe(0);
  });
});

describe("countReachedUsers", () => {
  it("counts users with any reached period once", () => {
    expect(
      countReachedUsers([
        { user_id: "a", daily: period(20, 20.31), weekly: period(50, 60), monthly: null },
        { user_id: "b", daily: period(10, 4.85), weekly: null, monthly: null },
      ]),
    ).toBe(1);
  });
});

describe("budgetToInput / scopeIsEmpty / parseBudgetField", () => {
  it("maps limits to *_usd inputs and null scopes to empty inputs", () => {
    expect(
      budgetToInput({
        runtime: { daily: period(20, 1), weekly: null, monthly: null },
        users: [{ user_id: "a", daily: null, weekly: period(50, 1), monthly: null }],
        can_manage: true,
      }),
    ).toEqual({
      runtime: { daily_usd: 20, weekly_usd: null, monthly_usd: null },
      users: [{ user_id: "a", daily_usd: null, weekly_usd: 50, monthly_usd: null }],
    });
    expect(budgetToInput({ runtime: null, users: [], can_manage: false })).toEqual({
      runtime: { daily_usd: null, weekly_usd: null, monthly_usd: null },
      users: [],
    });
  });

  it("treats blank as no limit and rejects non-positive or >2-decimal input", () => {
    expect(parseBudgetField("")).toBeNull();
    expect(parseBudgetField("  ")).toBeNull();
    expect(parseBudgetField("20")).toBe(20);
    expect(parseBudgetField("0.07")).toBe(0.07);
    expect(parseBudgetField("0")).toBeUndefined();
    expect(parseBudgetField("-1")).toBeUndefined();
    expect(parseBudgetField("1.005")).toBeUndefined();
    expect(parseBudgetField("abc")).toBeUndefined();
    expect(parseBudgetField("1000001")).toBeUndefined();
  });

  it("scopeIsEmpty is true only when all three limits are null", () => {
    expect(scopeIsEmpty({ daily_usd: null, weekly_usd: null, monthly_usd: null })).toBe(true);
    expect(scopeIsEmpty({ daily_usd: 1, weekly_usd: null, monthly_usd: null })).toBe(false);
  });
});
```

Run: `pnpm --filter @multica/views exec vitest run runtimes/budget.test.ts`
Expected: FAIL, module not found.

- [ ] **Step 2: Implement budget.ts**

`packages/views/runtimes/budget.ts`:

```ts
import type {
  RuntimeBudgetPeriod,
  RuntimeBudgetScope,
  RuntimeBudgetScopeInput,
  RuntimeCostBudget,
  RuntimeCostBudgetInput,
} from "@multica/core/types";

export const MAX_BUDGET_USD = 1_000_000;

// Fill ratio of a meter, 0..100. A reached period always fills the bar even
// when used_usd overshoots the limit.
export function budgetPercent(p: RuntimeBudgetPeriod | null): number {
  if (!p || p.limit_usd <= 0) return 0;
  if (p.reached === true) return 100;
  return Math.min(100, (p.used_usd / p.limit_usd) * 100);
}

export function scopeHasReached(s: RuntimeBudgetScope): boolean {
  return s.daily?.reached === true || s.weekly?.reached === true || s.monthly?.reached === true;
}

export function countReachedUsers(users: RuntimeBudgetScope[]): number {
  return users.filter(scopeHasReached).length;
}

function scopeToInput(s: RuntimeBudgetScope | null): RuntimeBudgetScopeInput {
  return {
    ...(s?.user_id ? { user_id: s.user_id } : {}),
    daily_usd: s?.daily?.limit_usd ?? null,
    weekly_usd: s?.weekly?.limit_usd ?? null,
    monthly_usd: s?.monthly?.limit_usd ?? null,
  };
}

// Editor seed: the runtime scope always exists as a row of inputs even when
// the server has no total row yet.
export function budgetToInput(b: RuntimeCostBudget): RuntimeCostBudgetInput {
  return {
    runtime: scopeToInput(b.runtime),
    users: (b.users ?? []).map(scopeToInput),
  };
}

// "" -> null (no limit); a valid positive amount with <= 2 decimals -> number;
// anything else -> undefined (invalid, block save).
export function parseBudgetField(raw: string): number | null | undefined {
  const trimmed = raw.trim();
  if (trimmed === "") return null;
  if (!/^\d+(\.\d{1,2})?$/.test(trimmed)) return undefined;
  const value = Number(trimmed);
  if (!Number.isFinite(value) || value <= 0 || value > MAX_BUDGET_USD) return undefined;
  return value;
}

export function scopeIsEmpty(s: RuntimeBudgetScopeInput): boolean {
  return s.daily_usd === null && s.weekly_usd === null && s.monthly_usd === null;
}
```

Run the helper test: PASS.

- [ ] **Step 3: Add locale keys**

Add a top-level `"budget"` object to `packages/views/locales/en/runtimes.json`:

```json
"budget": {
  "title": "Cost budget",
  "description": "Limits reset daily, weekly (Monday) and monthly at 00:00 UTC. Usage the server cannot price is not counted, so spend is a lower bound.",
  "edit_button": "Edit budget",
  "set_button": "Set budget",
  "empty_title": "No limits set",
  "empty_body": "Every user can spend without a cap on this runtime.",
  "col_scope": "Scope",
  "col_daily": "Daily",
  "col_weekly": "Weekly",
  "col_monthly": "Monthly",
  "runtime_total": "Runtime total",
  "runtime_total_hint": "All users on this runtime",
  "member_hint": "Member",
  "unlimited": "Unlimited",
  "used_of_limit": "{{used}} / {{limit}}",
  "resets_at": "resets {{when}}",
  "limit_reached": "Limit reached",
  "show_members_one": "Show {{count}} member budget",
  "show_members_other": "Show {{count}} member budgets",
  "hide_members": "Hide member budgets",
  "reached_badge_one": "{{count}} limit reached",
  "reached_badge_other": "{{count}} limits reached",
  "former_member": "Former member",
  "dialog": {
    "title": "Edit cost budget",
    "description": "USD per period. Leave a field empty for no limit. A reached limit refuses new runs until the period resets.",
    "runtime_hint": "Blocks everyone when reached",
    "add_member": "Add member",
    "remove_aria": "Remove member budget",
    "clear_hint": "Clearing every field on a member row removes it.",
    "no_limit_placeholder": "No limit",
    "invalid_amount": "Enter a positive USD amount with at most two decimals.",
    "save_failed": "Could not save the budget.",
    "cancel": "Cancel",
    "save": "Save"
  }
}
```

Add the same keys to `zh-Hans`, `ja`, `ko` with translated values (`zh-Hans` only needs the `_other` plural forms; `ja`/`ko` follow whichever plural forms their existing `cost_by_caption_owner_*` keys use). Simplified Chinese values:

```json
"budget": {
  "title": "费用额度",
  "description": "额度按 UTC 每日、每周（周一）、每月 00:00 重置。服务端无法计价的用量不计入，因此已用金额是下限。",
  "edit_button": "编辑额度",
  "set_button": "设置额度",
  "empty_title": "未设置额度",
  "empty_body": "所有用户在此 Runtime 上均不受额度限制。",
  "col_scope": "范围",
  "col_daily": "每日",
  "col_weekly": "每周",
  "col_monthly": "每月",
  "runtime_total": "Runtime 总额度",
  "runtime_total_hint": "此 Runtime 上的全部用户",
  "member_hint": "成员",
  "unlimited": "不限",
  "used_of_limit": "{{used}} / {{limit}}",
  "resets_at": "{{when}} 重置",
  "limit_reached": "已达上限",
  "show_members_other": "展开 {{count}} 位成员额度",
  "hide_members": "收起成员额度",
  "reached_badge_other": "{{count}} 项已达上限",
  "former_member": "已离开的成员",
  "dialog": {
    "title": "编辑费用额度",
    "description": "单位为每周期美元。留空表示不限额。达到上限后新任务将被拒绝，直到周期重置。",
    "runtime_hint": "达到上限时阻断所有用户",
    "add_member": "添加成员",
    "remove_aria": "移除成员额度",
    "clear_hint": "清空成员行的全部字段即移除该行。",
    "no_limit_placeholder": "不限",
    "invalid_amount": "请输入最多两位小数的正数美元金额。",
    "save_failed": "额度保存失败。",
    "cancel": "取消",
    "save": "保存"
  }
}
```

Run: `pnpm --filter @multica/views exec vitest run locales/parity.test.ts`
Expected: PASS.

- [ ] **Step 4: Write the failing BudgetSection test**

`packages/views/runtimes/components/budget-section.test.tsx`:

```tsx
// @vitest-environment jsdom

import type { ReactNode } from "react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import type { AgentRuntime, RuntimeCostBudget } from "@multica/core/types";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enRuntimes from "../../locales/en/runtimes.json";

const TEST_RESOURCES = { en: { common: enCommon, runtimes: enRuntimes } };

const budgetState = vi.hoisted(() => ({
  data: undefined as RuntimeCostBudget | undefined,
  role: "member" as "owner" | "admin" | "member",
}));

vi.mock("@multica/core/runtimes/queries", () => ({
  runtimeCostBudgetOptions: (rid: string) => ({ queryKey: ["runtimes", "budget", rid] }),
}));
vi.mock("@multica/core/workspace/queries", () => ({
  memberListOptions: () => ({ queryKey: ["members"] }),
}));
vi.mock("@tanstack/react-query", () => ({
  useQuery: (opts: { queryKey: unknown[] }) => {
    if (opts.queryKey[1] === "budget") return { data: budgetState.data, isLoading: false };
    return {
      data: [
        { user_id: "u-zhang", name: "张强", role: "owner", email: "", avatar_url: null },
        { user_id: "u-li", name: "Li Wei", role: "member", email: "", avatar_url: null },
      ],
      isLoading: false,
    };
  },
  useQueryClient: () => ({ invalidateQueries: vi.fn() }),
  useMutation: () => ({ mutateAsync: vi.fn(), isPending: false }),
}));
vi.mock("@multica/core/permissions/use-current-member", () => ({
  useCurrentMember: () => ({ userId: "u-zhang", role: budgetState.role, member: null, isLoading: false }),
}));
vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "ws-1" }));
vi.mock("../../common/actor-avatar", () => ({
  ActorAvatar: ({ actorId }: { actorId: string }) => <div data-testid={`avatar-${actorId}`} />,
}));

import { BudgetSection } from "./budget-section";

const runtime = { id: "rt-1", workspace_id: "ws-1", owner_id: "u-zhang" } as AgentRuntime;

const period = (limit: number, used: number) => ({
  limit_usd: limit, used_usd: used,
  period_start: "2026-09-02T00:00:00Z", reset_at: "2026-09-03T00:00:00Z",
  reached: used >= limit,
});

function wrap(ui: ReactNode) {
  return render(<I18nProvider locale="en" resources={TEST_RESOURCES}>{ui}</I18nProvider>);
}

describe("BudgetSection", () => {
  beforeEach(() => {
    budgetState.data = undefined;
    budgetState.role = "member";
  });

  it("renders the empty state and hides the edit button for members", () => {
    budgetState.data = { runtime: null, users: [], can_manage: false };
    wrap(<BudgetSection runtime={runtime} />);
    expect(screen.getByText("No limits set")).toBeTruthy();
    expect(screen.queryByRole("button", { name: /budget/i })).toBeNull();
  });

  it("shows Set budget for admins on the empty state", () => {
    budgetState.role = "admin";
    budgetState.data = { runtime: null, users: [], can_manage: true };
    wrap(<BudgetSection runtime={runtime} />);
    expect(screen.getByRole("button", { name: "Set budget" })).toBeTruthy();
  });

  it("is collapsed by default, shows the reached badge, and expands member rows", () => {
    budgetState.data = {
      runtime: { daily: period(60, 31.6), weekly: period(300, 118.4), monthly: null },
      users: [
        { user_id: "u-zhang", daily: period(20, 20.31), weekly: null, monthly: period(200, 64.1) },
        { user_id: "u-li", daily: period(10, 4.85), weekly: period(50, 22.9), monthly: null },
      ],
      can_manage: false,
    };
    wrap(<BudgetSection runtime={runtime} />);
    expect(screen.getByText("Runtime total")).toBeTruthy();
    expect(screen.queryByText("Li Wei")).toBeNull();
    const toggle = screen.getByRole("button", { name: /Show 2 member budgets/ });
    expect(toggle.textContent).toContain("1 limit reached");
    expect(screen.getAllByText("Unlimited").length).toBe(1);

    fireEvent.click(toggle);
    expect(screen.getByText("Li Wei")).toBeTruthy();
    expect(screen.getByText("张强")).toBeTruthy();
    expect(screen.getByText("Limit reached")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Hide member budgets" })).toBeTruthy();
  });

  it("omits the toggle when there are no member rows", () => {
    budgetState.data = { runtime: { daily: period(60, 1), weekly: null, monthly: null }, users: [], can_manage: false };
    wrap(<BudgetSection runtime={runtime} />);
    expect(screen.queryByRole("button", { name: /member budget/ })).toBeNull();
  });
});
```

Run: `pnpm --filter @multica/views exec vitest run runtimes/components/budget-section.test.tsx`
Expected: FAIL, module not found.

- [ ] **Step 5: Implement BudgetSection**

`packages/views/runtimes/components/budget-section.tsx`:

```tsx
"use client";

import { useState } from "react";
import { ChevronRight, Server } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import type {
  AgentRuntime,
  MemberWithUser,
  RuntimeBudgetPeriod,
  RuntimeBudgetScope,
  RuntimeCostBudget,
} from "@multica/core/types";
import { runtimeCostBudgetOptions } from "@multica/core/runtimes/queries";
import { memberListOptions } from "@multica/core/workspace/queries";
import { useCurrentMember } from "@multica/core/permissions/use-current-member";
import { canManageRuntimeBudget } from "@multica/core/permissions";
import { useWorkspaceId } from "@multica/core/hooks";
import { Button } from "@multica/ui/components/ui/button";
import { cn } from "@multica/ui/lib/utils";
import { ActorAvatar } from "../../common/actor-avatar";
import { useT } from "../../i18n";
import { formatUsd } from "../utils";
import { budgetPercent, countReachedUsers } from "../budget";
import { RuntimeBudgetDialog } from "./runtime-budget-dialog";

function formatReset(iso: string, locale: string): string {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return "";
  const sameDay = date.getTime() - Date.now() < 24 * 60 * 60 * 1000;
  return new Intl.DateTimeFormat(locale, sameDay
    ? { hour: "2-digit", minute: "2-digit" }
    : { weekday: "short", hour: "2-digit", minute: "2-digit" }).format(date);
}

function PeriodMeter({ period }: { period: RuntimeBudgetPeriod | null }) {
  const { t, i18n } = useT("runtimes");
  const locale = i18n.resolvedLanguage ?? i18n.language;
  if (!period) {
    return (
      <div className="flex flex-col gap-1.5">
        <div className="flex items-baseline justify-between gap-2">
          <span className="text-body font-medium tabular-nums">— <span className="font-normal text-muted-foreground">/ —</span></span>
          <span className="text-caption text-muted-foreground">{t(($) => $.budget.unlimited)}</span>
        </div>
        <div className="h-2 rounded-full bg-muted" />
      </div>
    );
  }
  const reached = period.reached === true;
  return (
    <div className="flex flex-col gap-1.5">
      <div className="flex items-baseline justify-between gap-2">
        <span className={cn("text-body font-medium tabular-nums", reached && "text-destructive")}>
          {formatUsd(period.used_usd)}{" "}
          <span className="font-normal text-muted-foreground">/ {formatUsd(period.limit_usd)}</span>
        </span>
        {reached ? (
          <span className="rounded-full bg-destructive/10 px-1.5 text-micro font-medium text-destructive">
            {t(($) => $.budget.limit_reached)}
          </span>
        ) : (
          <span className="text-caption text-muted-foreground">
            {t(($) => $.budget.resets_at, { when: formatReset(period.reset_at, locale) })}
          </span>
        )}
      </div>
      <div className="relative h-2 overflow-hidden rounded-full bg-muted">
        <div
          className={cn("h-full rounded-full", reached ? "bg-destructive" : "bg-chart-1")}
          style={{ width: `${budgetPercent(period)}%` }}
        />
      </div>
    </div>
  );
}

const ROW = "grid grid-cols-[220px_repeat(3,minmax(0,1fr))] items-center gap-6 px-4 py-3.5";

function ScopeRow({ scope, member, isRuntime }: { scope: RuntimeBudgetScope; member: MemberWithUser | null; isRuntime: boolean }) {
  const { t } = useT("runtimes");
  return (
    <div className={cn(ROW, "border-b")}>
      <div className="flex min-w-0 items-center gap-2">
        {isRuntime ? (
          <span className="flex h-6 w-6 shrink-0 items-center justify-center rounded-md border bg-card">
            <Server className="h-3.5 w-3.5" />
          </span>
        ) : scope.user_id ? (
          <ActorAvatar actorType="member" actorId={scope.user_id} size="sm" />
        ) : null}
        <div className="flex min-w-0 flex-col">
          <span className="truncate text-body font-medium">
            {isRuntime ? t(($) => $.budget.runtime_total) : member?.name ?? t(($) => $.budget.former_member)}
          </span>
          <span className="text-micro text-muted-foreground">
            {isRuntime ? t(($) => $.budget.runtime_total_hint) : t(($) => $.budget.member_hint)}
          </span>
        </div>
      </div>
      <PeriodMeter period={scope.daily} />
      <PeriodMeter period={scope.weekly} />
      <PeriodMeter period={scope.monthly} />
    </div>
  );
}

export function BudgetSection({ runtime }: { runtime: AgentRuntime }) {
  const { t } = useT("runtimes");
  const wsId = useWorkspaceId();
  const { data: budget } = useQuery(runtimeCostBudgetOptions(runtime.id));
  const { data: members = [] } = useQuery(memberListOptions(wsId));
  const { userId, role } = useCurrentMember(wsId);
  const [expanded, setExpanded] = useState(false);
  const [dialogOpen, setDialogOpen] = useState(false);

  // Both signals must agree: the local rule keeps the button off for members
  // even if a drifted backend claims can_manage, and vice versa.
  const canManage = canManageRuntimeBudget({ userId, role }).allowed && budget?.can_manage === true;
  const users = budget?.users ?? [];
  const hasAny = budget?.runtime != null || users.length > 0;
  const reachedCount = countReachedUsers(users);
  const memberById = new Map(members.map((m) => [m.user_id, m]));

  return (
    <div className="rounded-lg border bg-card">
      <div className="flex items-start justify-between gap-3 border-b p-4">
        <div className="min-w-0">
          <h3 className="text-title-sm font-semibold tracking-tight">{t(($) => $.budget.title)}</h3>
          <p className="text-caption text-muted-foreground">{t(($) => $.budget.description)}</p>
        </div>
        {canManage && (
          <Button type="button" variant="outline" size="sm" onClick={() => setDialogOpen(true)}>
            {hasAny ? t(($) => $.budget.edit_button) : t(($) => $.budget.set_button)}
          </Button>
        )}
      </div>

      {!hasAny ? (
        <div className="flex flex-col items-center gap-1 px-4 py-7">
          <p className="text-body font-medium">{t(($) => $.budget.empty_title)}</p>
          <p className="text-caption text-muted-foreground">{t(($) => $.budget.empty_body)}</p>
        </div>
      ) : (
        <>
          <div className={cn(ROW, "border-b py-2 text-micro uppercase tracking-wider text-muted-foreground")}>
            <div>{t(($) => $.budget.col_scope)}</div>
            <div>{t(($) => $.budget.col_daily)}</div>
            <div>{t(($) => $.budget.col_weekly)}</div>
            <div>{t(($) => $.budget.col_monthly)}</div>
          </div>
          {budget?.runtime && <ScopeRow scope={budget.runtime} member={null} isRuntime />}
          {expanded &&
            users.map((u) => (
              <ScopeRow key={u.user_id ?? "no-user"} scope={u} member={u.user_id ? memberById.get(u.user_id) ?? null : null} isRuntime={false} />
            ))}
          {users.length > 0 && (
            <button
              type="button"
              onClick={() => setExpanded((v) => !v)}
              className="flex w-full items-center gap-2 px-4 py-2.5 text-left text-body text-muted-foreground transition-colors hover:bg-muted/40 hover:text-foreground"
            >
              <ChevronRight className={cn("h-3.5 w-3.5 transition-transform", expanded && "rotate-90")} />
              <span>
                {expanded
                  ? t(($) => $.budget.hide_members)
                  : t(($) => $.budget.show_members, { count: users.length })}
              </span>
              {!expanded && reachedCount > 0 && (
                <span className="rounded-full bg-destructive/10 px-1.5 text-micro font-medium text-destructive">
                  {t(($) => $.budget.reached_badge, { count: reachedCount })}
                </span>
              )}
            </button>
          )}
        </>
      )}

      {canManage && budget && (
        <RuntimeBudgetDialog
          open={dialogOpen}
          onOpenChange={setDialogOpen}
          runtimeId={runtime.id}
          budget={budget}
          members={members}
        />
      )}
    </div>
  );
}
```

Check the `cn` import path used by sibling components (`grep -rn "import { cn }" packages/views/runtimes/components/usage-section.tsx`) and match it. If the last user row should not carry a border before the toggle, keep it: the toggle row supplies the top edge visually and the mockup shows a divider there.

- [ ] **Step 6: Run the BudgetSection test**

Run: `pnpm --filter @multica/views exec vitest run runtimes/components/budget-section.test.tsx`
Expected: FAIL only on the missing `./runtime-budget-dialog` module. Proceed to the dialog.

- [ ] **Step 7: Write the failing dialog test**

`packages/views/runtimes/components/runtime-budget-dialog.test.tsx`:

```tsx
// @vitest-environment jsdom

import type { ReactNode } from "react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import type { MemberWithUser, RuntimeCostBudget } from "@multica/core/types";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enRuntimes from "../../locales/en/runtimes.json";

const TEST_RESOURCES = { en: { common: enCommon, runtimes: enRuntimes } };

const mutateAsync = vi.hoisted(() => vi.fn(async () => ({})));
vi.mock("@multica/core/runtimes/mutations", () => ({
  useUpdateRuntimeCostBudget: () => ({ mutateAsync, isPending: false }),
}));
vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "ws-1" }));
vi.mock("../../common/actor-avatar", () => ({
  ActorAvatar: ({ actorId }: { actorId: string }) => <div data-testid={`avatar-${actorId}`} />,
}));

import { RuntimeBudgetDialog } from "./runtime-budget-dialog";

const members = [
  { user_id: "u-zhang", name: "张强", role: "owner", email: "", avatar_url: null },
  { user_id: "u-li", name: "Li Wei", role: "member", email: "", avatar_url: null },
] as MemberWithUser[];

const period = (limit: number) => ({ limit_usd: limit, used_usd: 0, period_start: "", reset_at: "", reached: false });

const budget: RuntimeCostBudget = {
  runtime: { daily: period(60), weekly: period(300), monthly: null },
  users: [{ user_id: "u-zhang", daily: period(20), weekly: null, monthly: period(200) }],
  can_manage: true,
};

function wrap(ui: ReactNode) {
  return render(<I18nProvider locale="en" resources={TEST_RESOURCES}>{ui}</I18nProvider>);
}

describe("RuntimeBudgetDialog", () => {
  beforeEach(() => mutateAsync.mockClear());

  it("seeds inputs from the budget and saves the full replacement", async () => {
    const onOpenChange = vi.fn();
    wrap(<RuntimeBudgetDialog open onOpenChange={onOpenChange} runtimeId="rt-1" budget={budget} members={members} />);
    expect((screen.getByLabelText("Runtime total daily") as HTMLInputElement).value).toBe("60");
    fireEvent.change(screen.getByLabelText("Runtime total monthly"), { target: { value: "500" } });
    fireEvent.change(screen.getByLabelText("张强 daily"), { target: { value: "" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(mutateAsync).toHaveBeenCalledTimes(1));
    expect(mutateAsync).toHaveBeenCalledWith({
      runtimeId: "rt-1",
      input: {
        runtime: { daily_usd: 60, weekly_usd: 300, monthly_usd: 500 },
        users: [{ user_id: "u-zhang", daily_usd: null, weekly_usd: null, monthly_usd: 200 }],
      },
    });
    await waitFor(() => expect(onOpenChange).toHaveBeenCalledWith(false));
  });

  it("drops a member row whose fields are all cleared and blocks invalid amounts", async () => {
    wrap(<RuntimeBudgetDialog open onOpenChange={vi.fn()} runtimeId="rt-1" budget={budget} members={members} />);
    fireEvent.change(screen.getByLabelText("张强 monthly"), { target: { value: "" } });
    fireEvent.change(screen.getByLabelText("Runtime total daily"), { target: { value: "-5" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(mutateAsync).not.toHaveBeenCalled();
    expect(screen.getByText("Enter a positive USD amount with at most two decimals.")).toBeTruthy();

    fireEvent.change(screen.getByLabelText("Runtime total daily"), { target: { value: "5" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(mutateAsync).toHaveBeenCalledTimes(1));
    expect(mutateAsync.mock.calls[0]?.[0]).toEqual({
      runtimeId: "rt-1",
      input: { runtime: { daily_usd: 5, weekly_usd: 300, monthly_usd: null }, users: [] },
    });
  });

  it("adds a member row from the picker", () => {
    wrap(<RuntimeBudgetDialog open onOpenChange={vi.fn()} runtimeId="rt-1" budget={budget} members={members} />);
    fireEvent.click(screen.getByRole("button", { name: "Add member" }));
    fireEvent.click(screen.getByRole("option", { name: "Li Wei" }));
    expect(screen.getByLabelText("Li Wei daily")).toBeTruthy();
  });
});
```

Run: `pnpm --filter @multica/views exec vitest run runtimes/components/runtime-budget-dialog.test.tsx`
Expected: FAIL, module not found.

- [ ] **Step 8: Implement the dialog**

`packages/views/runtimes/components/runtime-budget-dialog.tsx`:

```tsx
"use client";

import { useEffect, useMemo, useState } from "react";
import { Plus, Server, X } from "lucide-react";
import type {
  MemberWithUser,
  RuntimeBudgetPeriodKey,
  RuntimeCostBudget,
  RuntimeCostBudgetInput,
} from "@multica/core/types";
import { useUpdateRuntimeCostBudget } from "@multica/core/runtimes/mutations";
import { useWorkspaceId } from "@multica/core/hooks";
import { Button } from "@multica/ui/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { Input } from "@multica/ui/components/ui/input";
import { ActorAvatar } from "../../common/actor-avatar";
import { useT } from "../../i18n";
import { budgetToInput, parseBudgetField, scopeIsEmpty } from "../budget";

const PERIODS: RuntimeBudgetPeriodKey[] = ["daily", "weekly", "monthly"];

type Draft = Record<RuntimeBudgetPeriodKey, string>;
type UserDraft = Draft & { user_id: string };

function toDraft(limits: { daily_usd: number | null; weekly_usd: number | null; monthly_usd: number | null }): Draft {
  const s = (v: number | null) => (v === null ? "" : String(v));
  return { daily: s(limits.daily_usd), weekly: s(limits.weekly_usd), monthly: s(limits.monthly_usd) };
}

export function RuntimeBudgetDialog({
  open,
  onOpenChange,
  runtimeId,
  budget,
  members,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  runtimeId: string;
  budget: RuntimeCostBudget;
  members: MemberWithUser[];
}) {
  const { t } = useT("runtimes");
  const wsId = useWorkspaceId();
  const update = useUpdateRuntimeCostBudget(wsId);
  const seed = useMemo(() => budgetToInput(budget), [budget]);
  const [runtimeDraft, setRuntimeDraft] = useState<Draft>(() => toDraft(seed.runtime ?? { daily_usd: null, weekly_usd: null, monthly_usd: null }));
  const [userDrafts, setUserDrafts] = useState<UserDraft[]>(() =>
    seed.users.map((u) => ({ user_id: u.user_id ?? "", ...toDraft(u) })),
  );
  const [pickerOpen, setPickerOpen] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Re-seed whenever the dialog opens so a stale draft never overwrites a
  // budget another admin saved meanwhile.
  useEffect(() => {
    if (!open) return;
    setRuntimeDraft(toDraft(seed.runtime ?? { daily_usd: null, weekly_usd: null, monthly_usd: null }));
    setUserDrafts(seed.users.map((u) => ({ user_id: u.user_id ?? "", ...toDraft(u) })));
    setError(null);
    setPickerOpen(false);
  }, [open, seed]);

  const memberById = new Map(members.map((m) => [m.user_id, m]));
  const available = members.filter((m) => !userDrafts.some((d) => d.user_id === m.user_id));

  const handleSave = async () => {
    const parseScope = (d: Draft) => {
      const daily = parseBudgetField(d.daily);
      const weekly = parseBudgetField(d.weekly);
      const monthly = parseBudgetField(d.monthly);
      if (daily === undefined || weekly === undefined || monthly === undefined) return undefined;
      return { daily_usd: daily, weekly_usd: weekly, monthly_usd: monthly };
    };
    const runtime = parseScope(runtimeDraft);
    if (!runtime) {
      setError(t(($) => $.budget.dialog.invalid_amount));
      return;
    }
    const users: RuntimeCostBudgetInput["users"] = [];
    for (const d of userDrafts) {
      const scope = parseScope(d);
      if (!scope) {
        setError(t(($) => $.budget.dialog.invalid_amount));
        return;
      }
      if (scopeIsEmpty(scope)) continue;
      users.push({ user_id: d.user_id, ...scope });
    }
    setError(null);
    try {
      await update.mutateAsync({ runtimeId, input: { runtime: scopeIsEmpty(runtime) ? null : runtime, users } });
      onOpenChange(false);
    } catch {
      setError(t(($) => $.budget.dialog.save_failed));
    }
  };

  const renderInputs = (label: string, draft: Draft, onChange: (p: RuntimeBudgetPeriodKey, v: string) => void) =>
    PERIODS.map((p) => (
      <Input
        key={p}
        inputMode="decimal"
        aria-label={`${label} ${p}`}
        placeholder={t(($) => $.budget.dialog.no_limit_placeholder)}
        value={draft[p]}
        onChange={(e) => onChange(p, e.target.value)}
      />
    ));

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{t(($) => $.budget.dialog.title)}</DialogTitle>
          <DialogDescription>{t(($) => $.budget.dialog.description)}</DialogDescription>
        </DialogHeader>

        <div className="space-y-2">
          <div className="grid grid-cols-[minmax(0,1.3fr)_repeat(3,minmax(0,1fr))_1.75rem] gap-2 px-1 text-micro uppercase tracking-wider text-muted-foreground">
            <div>{t(($) => $.budget.col_scope)}</div>
            <div>{t(($) => $.budget.col_daily)}</div>
            <div>{t(($) => $.budget.col_weekly)}</div>
            <div>{t(($) => $.budget.col_monthly)}</div>
            <div />
          </div>

          <div className="grid grid-cols-[minmax(0,1.3fr)_repeat(3,minmax(0,1fr))_1.75rem] items-center gap-2 border-t px-1 py-2">
            <div className="flex min-w-0 items-center gap-2">
              <span className="flex h-6 w-6 shrink-0 items-center justify-center rounded-md border bg-card">
                <Server className="h-3.5 w-3.5" />
              </span>
              <div className="flex min-w-0 flex-col">
                <span className="text-body font-medium">{t(($) => $.budget.runtime_total)}</span>
                <span className="text-micro text-muted-foreground">{t(($) => $.budget.dialog.runtime_hint)}</span>
              </div>
            </div>
            {renderInputs(t(($) => $.budget.runtime_total), runtimeDraft, (p, v) => setRuntimeDraft((d) => ({ ...d, [p]: v })))}
            <div />
          </div>

          {userDrafts.map((d, i) => {
            const member = memberById.get(d.user_id);
            const name = member?.name ?? t(($) => $.budget.former_member);
            return (
              <div key={d.user_id} className="grid grid-cols-[minmax(0,1.3fr)_repeat(3,minmax(0,1fr))_1.75rem] items-center gap-2 border-t px-1 py-2">
                <div className="flex min-w-0 items-center gap-2">
                  <ActorAvatar actorType="member" actorId={d.user_id} size="sm" />
                  <div className="flex min-w-0 flex-col">
                    <span className="truncate text-body font-medium">{name}</span>
                    <span className="text-micro text-muted-foreground">{t(($) => $.budget.member_hint)}</span>
                  </div>
                </div>
                {renderInputs(name, d, (p, v) =>
                  setUserDrafts((all) => all.map((row, j) => (j === i ? { ...row, [p]: v } : row))),
                )}
                <Button
                  type="button"
                  variant="ghost"
                  size="icon"
                  className="size-7"
                  aria-label={t(($) => $.budget.dialog.remove_aria)}
                  onClick={() => setUserDrafts((all) => all.filter((_, j) => j !== i))}
                >
                  <X className="h-3.5 w-3.5" />
                </Button>
              </div>
            );
          })}

          <div className="flex items-center justify-between gap-2 border-t px-1 pt-2">
            <div className="relative">
              <Button type="button" variant="outline" size="sm" onClick={() => setPickerOpen((v) => !v)} disabled={available.length === 0}>
                <Plus className="h-3.5 w-3.5" />
                {t(($) => $.budget.dialog.add_member)}
              </Button>
              {pickerOpen && (
                <ul role="listbox" className="absolute left-0 top-full z-10 mt-1 max-h-56 w-64 overflow-y-auto rounded-lg border bg-popover p-1 shadow-md">
                  {available.map((m) => (
                    <li
                      key={m.user_id}
                      role="option"
                      aria-selected={false}
                      className="cursor-pointer rounded-md px-2 py-1.5 text-body hover:bg-muted"
                      onClick={() => {
                        setUserDrafts((all) => [...all, { user_id: m.user_id, daily: "", weekly: "", monthly: "" }]);
                        setPickerOpen(false);
                      }}
                    >
                      {m.name}
                    </li>
                  ))}
                </ul>
              )}
            </div>
            <span className="text-micro text-muted-foreground">{t(($) => $.budget.dialog.clear_hint)}</span>
          </div>

          {error && <p className="text-caption text-destructive">{error}</p>}
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>{t(($) => $.budget.dialog.cancel)}</Button>
          <Button onClick={handleSave} disabled={update.isPending}>{t(($) => $.budget.dialog.save)}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
```

If the repo has a shared member picker (`grep -rln "MemberPicker\|member-picker" packages/views`), use it in place of the inline listbox and keep the `role="option"` accessible name so the test's `getByRole("option", { name: "Li Wei" })` still resolves.

- [ ] **Step 9: Run both component tests**

Run: `pnpm --filter @multica/views exec vitest run runtimes/components/budget-section.test.tsx runtimes/components/runtime-budget-dialog.test.tsx`
Expected: PASS.

- [ ] **Step 10: Mount in runtime-detail**

In `packages/views/runtimes/components/runtime-detail.tsx`, import `BudgetSection` from `./budget-section` and change the main column to:

```tsx
            {canReadRuntime && <BudgetSection runtime={runtime} />}
            {canReadRuntime && <UsageSection runtime={runtime} />}
```

- [ ] **Step 11: Full views verification**

Run: `pnpm --filter @multica/views typecheck && pnpm --filter @multica/views lint && pnpm --filter @multica/views test`
Expected: PASS (the whole package suite, not just the new files).

- [ ] **Step 12: Commit**

```bash
git add packages/views/runtimes packages/views/locales
git commit -m "feat(runtimes): cost budget section and editor on the runtime detail page"
```

---

### Task 8: Surface `budget_exceeded` and the inbox notice in the client

**Files:**
- Modify: `packages/views/issues/blocked-trigger-copy.ts` (both switches), `packages/views/locales/*/issues.json` (`comment.trigger_blocked_budget_exceeded`, `comment.trigger_blocked_short_budget_exceeded`)
- Modify: `packages/views/autopilots/components/run-now-toast.ts` (`RunNowBlockedKey` union + switch), `packages/views/locales/*/autopilots.json` (`detail.run_blocked_budget_exceeded`)
- Modify: `packages/views/inbox/components/inbox-display.ts`, `inbox-detail-label.tsx`, `packages/views/locales/*/inbox.json` (`types.runtime_budget_exceeded`, `labels.runtime_budget_blocked`)
- Modify: tests beside each: `blocked-trigger-copy.test.ts` (create if absent), `run-now-toast.test.ts`, `inbox-display.test.ts`

- [ ] **Step 1: Write the failing tests**

Append to `packages/views/autopilots/components/run-now-toast.test.ts`:

```ts
  it("maps budget_exceeded to its own key", () => {
    expect(runNowBlockedKey("budget_exceeded")).toBe("run_blocked_budget_exceeded");
  });
```

Append to `packages/views/inbox/components/inbox-display.test.ts`:

```ts
  it("recognises the runtime budget notice", () => {
    expect(isRuntimeBudgetNotice("runtime_budget_exceeded")).toBe(true);
    expect(isRuntimeBudgetNotice("task_failed")).toBe(false);
  });
```

Create or extend `packages/views/issues/blocked-trigger-copy.test.ts`:

```ts
// @vitest-environment node
import { describe, expect, it } from "vitest";
import { blockedReasonLabel, blockedShortReasonLabel } from "./blocked-trigger-copy";

// t returns the selector's key path so the test asserts which key was chosen.
const t = ((sel: (root: Record<string, unknown>) => unknown) => {
  const proxy: Record<string, unknown> = new Proxy({}, {
    get: (_o, key) => new Proxy({}, { get: (_o2, key2) => `${String(key)}.${String(key2)}` }),
  });
  return sel(proxy);
}) as unknown as Parameters<typeof blockedReasonLabel>[1];

describe("blocked trigger copy", () => {
  it("has budget_exceeded copy in both lengths", () => {
    expect(blockedReasonLabel("budget_exceeded", t)).toBe("comment.trigger_blocked_budget_exceeded");
    expect(blockedShortReasonLabel("budget_exceeded", t)).toBe("comment.trigger_blocked_short_budget_exceeded");
  });
  it("keeps the generic fallback for unknown codes", () => {
    expect(blockedReasonLabel("something_new", t)).toBe("comment.trigger_blocked_generic");
  });
});
```

Run: `pnpm --filter @multica/views exec vitest run issues/blocked-trigger-copy.test.ts autopilots/components/run-now-toast.test.ts inbox/components/inbox-display.test.ts`
Expected: FAIL on the new assertions.

- [ ] **Step 2: Implement**

`blocked-trigger-copy.ts`: add before each `default:`

```ts
    case "budget_exceeded":
      return t(($) => $.comment.trigger_blocked_budget_exceeded);
```
```ts
    case "budget_exceeded":
      return t(($) => $.comment.trigger_blocked_short_budget_exceeded);
```

`run-now-toast.ts`: add `| "run_blocked_budget_exceeded"` to the union and

```ts
    case "budget_exceeded":
      return "run_blocked_budget_exceeded";
```

`inbox-display.ts`:

```ts
export function isRuntimeBudgetNotice(type: InboxItem["type"]): boolean {
  return type === "runtime_budget_exceeded";
}
```

`inbox-detail-label.tsx`: add `runtime_budget_exceeded: t(($) => $.types.runtime_budget_exceeded),` to `useTypeLabels` and a case

```tsx
    case "runtime_budget_exceeded":
      return <span>{t(($) => $.labels.runtime_budget_blocked)}</span>;
```

The detail pane in `inbox-page.tsx` already renders `detailItem.body` for unknown types; the server body carries the scope, limit, used amount and UTC reset time, so no dedicated notice component is needed.

Locale values (`en`):

- `issues.json` → `comment.trigger_blocked_budget_exceeded`: `"This target's runtime has reached its cost budget — try again after the period resets"`; `comment.trigger_blocked_short_budget_exceeded`: `"Budget reached"`.
- `autopilots.json` → `detail.run_blocked_budget_exceeded`: `"The runtime's cost budget is reached; the run will be possible after the period resets."`
- `inbox.json` → `types.runtime_budget_exceeded`: `"Runtime cost budget reached"`; `labels.runtime_budget_blocked`: `"New runs are refused until the period resets"`.

`zh-Hans`:

- `comment.trigger_blocked_budget_exceeded`: `"该目标所在 Runtime 已达费用额度，请在周期重置后重试"`; `comment.trigger_blocked_short_budget_exceeded`: `"额度已满"`.
- `detail.run_blocked_budget_exceeded`: `"Runtime 费用额度已达上限，周期重置后才能运行。"`
- `types.runtime_budget_exceeded`: `"Runtime 费用额度已达上限"`; `labels.runtime_budget_blocked`: `"新任务将被拒绝，直到周期重置"`.

Add `ja` and `ko` translations for the same keys.

- [ ] **Step 3: Verify**

Run: `pnpm --filter @multica/views test && pnpm --filter @multica/views typecheck && pnpm --filter @multica/views lint`
Expected: PASS including `locales/parity.test.ts`.

- [ ] **Step 4: Commit**

```bash
git add packages/views/issues packages/views/autopilots packages/views/inbox packages/views/locales
git commit -m "feat(views): localize budget_exceeded refusals and the runtime budget inbox notice"
```

---

### Task 9: Broad verification

**Files:** none new.

- [ ] **Step 1: Go**

Run: `cd server && go build ./... && go vet ./... && go test ./internal/pricing ./internal/metrics ./internal/dispatch ./internal/migrations ./cmd/migrate -count=1 && go test ./internal/service -run 'RuntimeCostBudget|RuntimeBudget|EnqueueTaskForIssueRefused|Attribution|Admission' -count=1 && go test ./internal/handler -run 'RuntimeCostBudget|DeleteRuntime|Runtime|Comment|QuickCreate|Chat' -count=1`
Expected: PASS. Then `make test`; report any failure that is not one of the known environmental ones in the memory notes.

- [ ] **Step 2: TypeScript**

Run: `pnpm typecheck && pnpm lint && pnpm test`
Expected: PASS.

- [ ] **Step 3: Manual smoke through the app**

Run `make up` and open the runtime detail page as the workspace owner: the Cost budget card shows the empty state with "Set budget"; set a runtime daily limit of 1 USD; seed spend by running a task or inserting a `task_usage` row with `cost_usd_ticks = 20_000_000_000` on a completed task of the runtime; reload and confirm the meter shows "Limit reached"; assign an issue to an agent on that runtime and confirm the comment chip reads "Budget reached" and an inbox item "Runtime cost budget reached" arrives for the owner. Log in as a member and confirm the card has no edit button.

- [ ] **Step 4: Commit any fix-ups and push the branch**

```bash
git status
git push -u origin feat/runtime-cost-budget --no-verify
```

Open the PR against `furtherref/multica` with a body that summarizes the spec (`gh pr create --repo furtherref/multica`), paragraphs as single lines.
