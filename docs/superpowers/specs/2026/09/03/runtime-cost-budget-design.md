# Runtime Cost Budget Design

## Goal

Let workspace owners and admins cap the estimated model spend of a runtime.
A runtime gets an optional total budget, and individual users can get their
own budget on that runtime. Each budget is a set of daily, weekly and monthly
USD limits; an empty limit means unlimited. When a limit is reached, new agent
runs that would spend against it are refused until the period rolls over.

## Decisions taken with the product owner

- Budgets attach to a runtime. The runtime total counts every task the
  runtime executes. A per-user budget counts the tasks on that runtime whose
  agent is owned by that user, matching the existing "Cost by owner" tab.
- Daily, weekly and monthly limits may be set together. Any reached limit
  blocks. The runtime total blocks everyone; a per-user limit blocks only that
  user's agents.
- Blocking is a hard refusal at enqueue time. The run is not queued. Periods
  reset on the calendar; a refused run is not retried automatically, the user
  triggers it again.
- Period boundaries are computed in UTC for every scope: a day is the UTC
  calendar day, a week starts Monday 00:00 UTC, a month starts on the first
  at 00:00 UTC. User and runtime timezone preferences do not affect budgets.
  The UI shows reset times in the viewer's timezone but labels the period
  as UTC so a reset at 08:00 local is not read as a bug.
- Spend that the server cannot price does not count. Provider-reported cost
  (`task_usage.cost_usd_ticks`) is authoritative. Rows without it are priced
  with the server rate table; models the server cannot price count as zero.
  Browser-local custom prices never affect enforcement. Missing Copilot
  telemetry therefore under-counts, and the UI says the figure is a lower
  bound.

## Current state

Cost is computed only in the browser. `estimateCost` in
`packages/views/runtimes/utils.ts` prices `RuntimeUsage` rows from a
TypeScript rate table plus a localStorage store of custom prices. The server
stores tokens and optional provider cost and has a second rate table in
`server/internal/metrics/pricing.go` that only feeds Prometheus. There is no
server-side USD figure, no budget concept, and no per-user usage rollup.
`ListRuntimeUsageByAgent` in `server/pkg/db/queries/runtime_usage.sql`
already aggregates cost buckets per agent for a runtime since a cutoff, which
is the shape a budget check needs.

Admission outcomes use `dispatch.ReasonCode`
(`server/internal/dispatch/reason.go`) carried from the service layer to
`DispatchOutcome` in `server/internal/handler/admission.go`. The
attribution refusal `ErrAttributionFailClosed` is the closest precedent for
an enqueue-time refusal that every trigger path must surface.

## Data model

Migration `452_runtime_cost_budget` creates:

```
runtime_cost_budget
  id                        UUID PRIMARY KEY
  workspace_id              UUID NOT NULL
  runtime_id                UUID NOT NULL
  user_id                   UUID NULL      -- NULL = runtime total
  daily_limit_usd_ticks     BIGINT NULL    -- 1e-10 USD, NULL = unlimited
  weekly_limit_usd_ticks    BIGINT NULL
  monthly_limit_usd_ticks   BIGINT NULL
  daily_notified_period_start   TIMESTAMPTZ NULL
  weekly_notified_period_start  TIMESTAMPTZ NULL
  monthly_notified_period_start TIMESTAMPTZ NULL
  updated_by                UUID NULL
  created_at, updated_at    TIMESTAMPTZ NOT NULL
```

No foreign keys. A separate single-statement migration builds
`CREATE UNIQUE INDEX CONCURRENTLY ... ON runtime_cost_budget (runtime_id, user_id) NULLS NOT DISTINCT`.
A row whose three limits are all NULL is deleted rather than kept, so
"absent row" is the only representation of unlimited. Runtime deletion and
member removal delete matching rows in application code.

Ticks reuse `CostUSDTicksPerUSD` so limits and spend share one unit.

## Server-side pricing

Move the rate table and `PriceForModelAlias` from `server/internal/metrics`
into a new leaf package `server/internal/pricing`; `metrics` imports it.
Add `pricing.EstimateCostTicks(provider, model, costTicks, uncosted tokens)`
which returns provider cost plus priced uncosted tokens, or provider cost
alone when the model is unpriced. This is the only cost formula the server
uses for budgets.

## Spend computation

A sqlc query `ListRuntimeSpendByOwner(runtime_id, daily_start,
weekly_start, monthly_start)` mirrors `ListRuntimeUsageByAgent` but
left-joins `agent`, groups by `agent.owner_id` alongside provider and model,
and computes all three period windows in one pass with
`FILTER (WHERE tu.created_at >= …)` aggregates. The three starts are not
ordered against each other — the current Monday can fall before the first of
the month — so the scan floors on their `LEAST` and each period picks its own
rows. Go folds the result once into a `runtimeSpend`: per period, a runtime
total and a map of `EstimateCostTicks` sums keyed by agent owner. Agents
nobody owns contribute to the total only.

One read therefore answers every scope and every period, instead of one
aggregate per (budget row, period): the budget endpoint of a runtime with N
per-user rows costs one spend query rather than 3N+3, and a check costs one
rather than up to six. Spend is still computed on demand for each check and
for the budget endpoint; no running counter is stored, so pricing table
updates and late usage reports never drift from the enforcement figure.

Period start is computed in Go: `pricing.PeriodStart(now, period)`
returns UTC midnight of today, of the current Monday, or of the first of
the month. `ResetAt` is the next boundary.

## Enforcement

`TaskService.checkRuntimeCostBudget(ctx, agent)` runs in every enqueue
helper right after attribution resolves: `enqueueIssueTaskWithCommentPlan`,
`enqueueMentionTaskWithCommentPlan`, `enqueueQuickCreateTask`,
`enqueueChatTaskTx` and `enqueueRerunTask`. It loads the budget rows for
`agent.runtime_id` in one query. With no rows it returns immediately, so
workspaces without budgets pay one indexed lookup per enqueue; a runtime
whose budgets all belong to other owners is just as cheap, because the spend
query is issued only once a row that applies to this agent is known.
Otherwise it loads the runtime's spend once with `ListRuntimeSpendByOwner`
and evaluates the runtime total row and the row for `agent.owner_id` against
it, each period with a limit, and returns
`*RuntimeBudgetExceededError{Scope, Period,
UsedTicks, LimitTicks, PeriodStart, ResetAt, UserID}` on the first reached
limit. Agents with no runtime skip the check.

A new `dispatch.ReasonBudgetExceeded = "budget_exceeded"` is added.
Handlers that type enqueue errors (`commentEnqueueFailureReason`, the chat,
quick-create, rerun and assign paths, and the autopilot attribution branch)
map the error with `errors.As` to that code; synchronous triggers answer
`409` through `writeDispatchBlocked`.

The enqueue helpers are not the only writers of `agent_task_queue`. Every
other path that mints a runnable task carries the same gate, placed where the
executing agent is known and before the row is written:

- **`dispatchRunOnly`** (autopilot `run_only`) resolves its own leader and
  calls `CreateAutopilotTask` directly. A reached budget returns
  `errDispatchSkipped{code: budget_exceeded}`, so the run is recorded
  `skipped` with that reason code — the same shape as the readiness and
  attribution gates beside it, and never a `failed` run. The manual
  "run now" response carries the code on a `200` with the skipped run, as it
  does for every other post-admission skip; `create_issue` autopilots are
  refused by the enqueue helper they call and reach `dispatchFailReasonCode`,
  which types the error as `budget_exceeded` for the same response field.
- **`RetrySourceContextQuickCreate`** (manual source-context retry) checks
  after the invoke gate and the issue-capacity preflight, before its
  transaction, and returns the error unchanged. The retry handler maps it to
  `409` + `budget_exceeded`, matching `writeSourceContextError` on the
  capture path.
- **`dispatchDelegatedFailureRecovery`** checks the coordinator's agent before
  creating the recovery task. A refusal creates nothing and returns no error:
  the durable recovery comment stays in the outbox, so a later sweep replays
  it once the period resets. The sweep counts it as `Blocked` — neither
  replayed nor exhausted.
- **Automatic retries** are suppressed by a reached budget, on both entry
  points (`FailTask`'s in-transaction child and `MaybeRetryFailedTask`), via
  the shared `retrySuppressedByRuntimeBudget` helper. **Ruling:** a reached
  budget stops automatic retries. The parent keeps its own failure reason,
  no child is created, and the log line names the budget as the suppressor.
  The check runs before the transaction on the auto-commit handle, so the
  notice survives; an unreadable budget also suppresses the retry, and never
  fails the parent's own transition. An unreadable *agent* suppresses it too:
  the agent row is what resolves the runtime whose budget gates the retry, so
  without it the budget was never consulted at all.
- **Deferred fallback tasks** are checked at **promotion**, not creation.
  **Ruling:** a fallback armed hours earlier is a prediction about a limit
  that may not have been reached yet; the only moment that matters is when it
  would start spending. `failDueDeferredTasksOverBudget` runs immediately
  before `PromoteDueDeferredTasksForRuntime(s)` in both the single- and
  multi-runtime claim paths: it prices each agent that owns a due deferred
  row and, for a blocked one, flips that agent's due rows to `failed` with
  `failure_reason = 'budget_exceeded'` (a new
  `taskfailure.ReasonBudgetExceeded`, sharing the dispatch reason's wire
  value) so the task is visible as a terminal outcome instead of held
  silently. One refusal never stops the sweep — every other agent's rows
  promote in the same tick — and an unreadable budget leaves that agent's
  rows to promote rather than retiring a member's work over a transient read.
  `PromoteDeferredChannelIssueTask` and `PromoteChannelChatTasksIfMediaReady`
  are untouched: they promote on media readiness, and the same rows' `fire_at`
  fallback still passes through the gated sweep.

Tasks already queued or running when a limit is reached are not interrupted
and are not filtered at claim time. Runtime deletion (every path, including
profile deletion and the offline-runtime sweeper), member revocation and
workspace deletion remove the runtime's budget rows in the same transaction. The overshoot is bounded by the
in-flight work at the moment the limit is crossed, and the claim path stays
untouched.

## Notification

On the first refusal in a period for a given row and period, the service
creates an inbox item of type `runtime_budget_exceeded` with severity
`attention`, details carrying scope, period, used, limit, reset time and
runtime id as strings. Recipients are the owner of the refused agent and the
runtime owner, de-duplicated, for both scopes. The notice runs on an
auto-commit connection, never inside the enqueue transaction (which the
refusal rolls back), and the runtime and recipients are resolved before the
`*_notified_period_start` claim so a failed lookup cannot consume a period.

The amounts stay inside that notice. `RuntimeBudgetExceededError.Error()`
names the limit, the running total and the reset time, and is used for server
logs only; every string that is persisted or broadcast beyond the notice's
recipients uses `PublicReason()` — `runtime cost budget reached (runtime
daily)` — instead. That covers `agent_task_queue.error` (republished to every
workspace subscriber on `task:failed`) and `autopilot_run.failure_reason`. A
`GET /budget` read is gated on runtime read access and `PUT` withholds spend
from a writer who may not read a private runtime, so a limit carried in one of
those columns would be a workspace-wide way around both. The `409`
`budget_exceeded` responses already answer with the generic
`dispatchBlockedFallbackMessage`, which carries no figures.

## API

`GET /api/runtimes/{id}/budget` (runtime read access):

```json
{
  "runtime": { "daily": { "limit_usd": 20, "used_usd": 3.42,
                          "period_start": "...", "reset_at": "...", "reached": false },
               "weekly": null, "monthly": null },
  "users": [ { "user_id": "...", "daily": null, "weekly": {...}, "monthly": null } ],
  "can_manage": true
}
```

`PUT /api/runtimes/{id}/budget` (workspace owner or admin via
`requireWorkspaceRole`) replaces the whole budget set:

```json
{ "runtime": { "daily_usd": 20, "weekly_usd": null, "monthly_usd": null },
  "users": [ { "user_id": "...", "daily_usd": null, "weekly_usd": 50, "monthly_usd": null } ] }
```

Validation: amounts are finite, greater than zero, at most 1,000,000 USD,
with at most two decimals; each `user_id` is a workspace member and appears
once. Rows that end up with no limits are deleted. The response is the
`GET` body, except when the writer cannot read the runtime (an admin on
another member's private runtime): then the reply is `204` with no body,
because the spend figures are the read that `GET` denies.

## Frontend

`packages/core`:

- `RuntimeBudgetSchema` parsed with `parseWithFallback`; types in
  `packages/core/types/agent.ts`; `getRuntimeBudget` and
  `updateRuntimeBudget` in the API client; `runtimeBudgetOptions(runtimeId)`
  under `runtimeKeys`; `useUpdateRuntimeBudget(wsId)` invalidating that key.
- `canManageRuntimeBudget(ctx)` in `permissions/rules.ts` reusing
  `isAdminLike`.
- `budget_exceeded` added wherever `attribution_blocked` is classified, in
  the chat send-failure toasts, and in the mobile dispatch-reason mapper;
  `runtime_budget_exceeded` added to the inbox item type union, the views
  inbox labels and title predicate, and the mobile inbox label record.

`packages/views/runtimes/components`:

- `BudgetSection` rendered in `runtime-detail.tsx` between the hero card and
  the usage section. It is one card with a column header (Scope, Daily,
  Weekly, Monthly) and one row per scope: a "Runtime total" row and one row
  per user with a budget, each with a daily, weekly and monthly meter
  (used / limit, progress bar, reset time). Unlimited periods render as a
  dash. Empty state says no limits are set. A "lower bound" hint appears
  whenever the usage coverage warning already shows.
- The card is collapsed by default and then shows only the "Runtime total"
  row. A footer toggle row styled like the hero card's "Technical details"
  row reads "Show N member budgets" and expands the user rows in place;
  expanded it reads "Hide member budgets". When any hidden user row has a
  reached limit, the collapsed toggle carries a "N limits reached" badge so
  the block is visible without expanding. The open/closed state is
  component state and is not persisted. With no user rows the toggle is
  omitted.
- `RuntimeBudgetDialog` opened from an "Edit budget" button visible only when
  `canManageRuntimeBudget`. It is a table with a fixed "Runtime total" row
  and member rows added through a member picker; three USD inputs per row;
  clearing all inputs on a member row removes it. Save calls the PUT
  mutation and closes on success.
- Inbox label for `runtime_budget_exceeded` and dispatch-blocked copy for
  `budget_exceeded` in the existing places.

Locale keys under `runtimes.budget.*` and the inbox and issues bundles in
`en`, `zh-Hans`, `ja` and `ko`.

## Testing

- `server/internal/pricing`: period boundary table across week starts,
  month ends and year ends in UTC; cost estimate with priced, unpriced and
  provider-costed rows.
- Service: enqueue refused for runtime total, refused for one user while
  another passes, passes with no rows, passes when spend is unpriced,
  notification fires once per period; the spend loader returns every period
  and owner from one call (including a week that starts before the month),
  and the budget endpoint issues exactly one spend query for three per-user
  scopes.
- Handler: `PUT` returns 403 for members, 200 for owner and admin, rejects
  invalid amounts and non-members, deletes emptied rows; `GET` reports used
  and reached.
- `packages/core`: schema malformed-response test, permission rule test,
  query key test.
- `packages/views`: `BudgetSection` empty and populated states, collapsed by
  default with the toggle expanding user rows and the reached badge, button
  gated by role, dialog save wiring.
