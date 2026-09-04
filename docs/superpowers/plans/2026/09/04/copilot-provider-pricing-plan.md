# Copilot Provider Pricing Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. This task explicitly prohibits subagents. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Price Copilot GPT-5.6 usage with date-aware GitHub Copilot rates in every UI aggregate and in server-side runtime budgets.

**Architecture:** Add provider-qualified, effective-dated Copilot rules to the existing TypeScript and Go pricing engines. Preserve a UTC `pricing_date` in aggregate endpoints, separate from the viewer-timezone display date, so each slice is priced before folding. Change runtime-budget spend to price UTC-dated rows before accumulating daily, weekly, and monthly totals. Long-context rules are represented but selected only when request-level input is explicitly supplied.

**Tech Stack:** TypeScript, Vitest, Go 1.26, PostgreSQL/sqlc, Zod.

---

### Task 1: Add date-aware Copilot pricing to the frontend

**Files:**
- Modify: `packages/views/runtimes/utils.test.ts`
- Modify: `packages/views/runtimes/utils.ts`

- [ ] **Step 1: Write failing provider/date/tier tests**

Add cases equivalent to:

```ts
expect(estimateCost({...tokens, provider: "copilot", model: "gpt-5.6-sol", pricing_date: "2026-09-03"})).toBeCloseTo(promoCost);
expect(estimateCost({...tokens, provider: "copilot", model: "gpt-5.6-sol", pricing_date: "2026-09-04"})).toBeCloseTo(standardCost);
expect(estimateCost({...tokens, provider: "codex", model: "gpt-5.6-sol", pricing_date: "2026-09-04"})).toBeCloseTo(openAICost);
```

Also call the pure rule resolver with request input exactly at and one token
above the 272K/200K thresholds. Aggregates without request-level input must use
the default tier.

- [ ] **Step 2: Verify RED**

```bash
pnpm --filter @multica/views exec vitest run runtimes/utils.test.ts
```

Expected: the Copilot cases receive the existing bare OpenAI prices and fail.

- [ ] **Step 3: Implement provider-qualified rules**

Add a small `CopilotPriceRule` catalog containing effective periods, default
rates, and optional long-context rates. Extend `Priceable` with optional `pricing_date`
and request-level input metadata. Resolve the qualified rule first, use the
usage date when present, and never derive the long tier from aggregate token
totals.

- [ ] **Step 4: Verify GREEN**

Run the command from Step 2 and expect all tests to pass.

### Task 2: Preserve UTC pricing dates in client-priced aggregate endpoints

**Files:**
- Modify: `server/pkg/db/queries/runtime_usage.sql`
- Modify: `server/pkg/db/queries/task_usage.sql`
- Modify: `server/pkg/db/generated/runtime_usage.sql.go`
- Modify: `server/pkg/db/generated/task_usage.sql.go`
- Modify: `server/internal/handler/runtime.go`
- Modify: `server/internal/handler/dashboard.go`
- Modify: `server/internal/handler/runtime_test.go`
- Modify: `server/internal/handler/dashboard_test.go`
- Modify: `packages/core/types/agent.ts`
- Modify: `packages/core/api/schemas.ts`
- Modify: `packages/core/api/schemas.test.ts`

- [ ] **Step 1: Write failing SQL/handler and schema tests**

Assert usage rows on opposite sides of UTC midnight remain separate by
`pricing_date`, even when they share one viewer-timezone display date. Assert
by-agent and by-hour rows return `pricing_date` plus `provider`. Assert old
responses with either field missing still parse to empty strings.

- [ ] **Step 2: Verify RED**

```bash
cd server && go test ./internal/handler -run 'Test.*Usage.*Date' -count=1
pnpm --filter @multica/core exec vitest run api/schemas.test.ts
```

Expected: pricing-date/provider fields or UTC-date grouping are absent.

- [ ] **Step 3: Add date dimensions and regenerate sqlc**

Add `DATE(bucket_hour AT TIME ZONE 'UTC') AS pricing_date` to hourly-rollup
queries and `DATE(created_at AT TIME ZONE 'UTC') AS pricing_date` to raw usage
queries, then include that expression in each grouping. Keep the existing
viewer-timezone `date`/`hour` dimensions unchanged. Return `pricing_date` and
provider from handlers, add them to client types and fallback schemas, and run:

```bash
make sqlc
```

- [ ] **Step 4: Verify GREEN**

Run the tests from Step 2 plus `pnpm --filter @multica/core typecheck`.

### Task 3: Make server runtime budgets provider- and date-aware

**Files:**
- Modify: `server/internal/pricing/pricing.go`
- Modify: `server/internal/pricing/pricing_test.go`
- Modify: `server/internal/pricing/cost.go`
- Modify: `server/internal/pricing/cost_test.go`
- Modify: `server/pkg/db/queries/runtime_cost_budget.sql`
- Modify: `server/pkg/db/generated/runtime_cost_budget.sql.go`
- Modify: `server/internal/service/runtime_cost_budget.go`
- Modify: `server/internal/service/runtime_cost_budget_test.go`

- [ ] **Step 1: Write failing Go pricing tests**

Test `PriceForUsage(provider, model, occurredAt, requestInputTokens)` for Sol's
promotion boundary, Copilot versus Codex, Terra/Luna cache rates, and explicit
long-context thresholds. Test `EstimateCostTicksForUsage` with the same dated
fixtures.

- [ ] **Step 2: Verify RED**

```bash
cd server && go test ./internal/pricing -run 'Test.*Copilot' -count=1
```

Expected: the provider-aware functions do not exist or return OpenAI rates.

- [ ] **Step 3: Implement the Go rules and dated spend rows**

Add provider/date-aware pricing functions while retaining `PriceForModelAlias`
for unrelated callers. Change `ListRuntimeSpendByOwner` to group by UTC usage
date with one cost/token set. In `loadRuntimeSpend`, price each row once and add
it only to periods whose start is on or before that row's date.

- [ ] **Step 4: Regenerate sqlc and verify GREEN**

```bash
make sqlc
cd server && go test ./internal/pricing ./internal/service -run 'Test.*Copilot|Test.*RuntimeCostBudget' -count=1
```

Expected: provider/date pricing and budget tests pass.

### Task 4: Verify parity and deliver

**Files:**
- Modify only files listed above plus this design and plan.

- [ ] **Step 1: Run focused and broad checks**

```bash
pnpm --filter @multica/views exec vitest run runtimes/utils.test.ts
pnpm --filter @multica/core exec vitest run api/schemas.test.ts
pnpm --filter @multica/views typecheck
pnpm --filter @multica/core typecheck
cd server && go test ./internal/pricing ./internal/service ./internal/handler -count=1
cd server && go vet ./internal/pricing ./internal/service ./internal/handler
git diff --check
```

- [ ] **Step 2: Compare frontend and backend golden cases**

The same 1M-token fixtures must produce identical Copilot rates and Sol
effective-date decisions in TypeScript and Go tests.

- [ ] **Step 3: Review and commit**

```bash
git add docs/superpowers packages/views/runtimes packages/core server
git commit -m "feat(usage): add Copilot provider pricing"
```
