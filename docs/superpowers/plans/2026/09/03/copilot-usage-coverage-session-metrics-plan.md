# Copilot Usage Coverage and Session Metrics Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Recover complete future Copilot token telemetry from local session snapshots and show runtime cost as a lower bound whenever completed runs have output-only or missing usage.

**Architecture:** A new agent-layer session snapshot reader supplies a mutually exclusive complete usage source ahead of legacy output-only stdout. A separate runtime coverage endpoint derives complete/output-only/missing run counts from existing queue and usage rows, preserving the existing usage API for older clients. Shared frontend schemas and queries expose coverage to the runtime page, which renders an explicit incomplete-data warning and lower-bound KPI.

**Tech Stack:** Go 1.26, sqlc/PostgreSQL, Chi, TypeScript, Zod, TanStack Query, React, Vitest, i18next.

---

### Task 1: Recover Copilot usage from session shutdown snapshots

**Files:**
- Create: `server/pkg/agent/copilot_session_usage.go`
- Create: `server/pkg/agent/copilot_session_usage_test.go`
- Modify: `server/pkg/agent/copilot.go`
- Modify: `server/pkg/agent/copilot_test.go`

- [ ] **Step 1: Write failing pure snapshot-reader tests**

Cover a fresh snapshot, a resumed delta, independent model keys, a partial
trailing line, unsafe session IDs, an absent baseline, and a counter regression.
Use a task-local `HOME` and real `events.jsonl` fixtures.

- [ ] **Step 2: Run the focused tests and verify RED**

Run:

```bash
cd server
go test ./pkg/agent -run 'TestCopilotSessionUsage' -count=1
```

Expected: FAIL because the snapshot reader and delta resolver do not exist.

- [ ] **Step 3: Implement the bounded session snapshot reader**

Add:

```go
type copilotUsageSnapshot map[string]copilotRawTokenUsage

func readCopilotSessionUsageSnapshot(env []string, sessionID string) (copilotUsageSnapshot, bool, error)
func diffCopilotUsageSnapshots(before, after copilotUsageSnapshot) (map[string]TokenUsage, error)
func freshCopilotUsageSnapshot(snapshot copilotUsageSnapshot) map[string]TokenUsage
```

Resolve `HOME` / `USERPROFILE` from the subprocess environment, validate the
session ID as a safe path component, read only a bounded file tail, select the
latest valid `session.shutdown`, and pass raw totals through `addUsage`.

- [ ] **Step 4: Run the pure tests and verify GREEN**

Run the focused command from Step 2. Expected: PASS.

- [ ] **Step 5: Write failing Execute-level precedence tests**

Use the existing fake Copilot executable pattern to prove:

- complete stdout usage wins over a session file;
- a fresh session file beats legacy output-only stdout;
- a resumed file delta excludes the baseline;
- an absent/unsafe file preserves legacy behavior.

- [ ] **Step 6: Run Execute tests and verify RED**

```bash
cd server
go test ./pkg/agent -run 'TestCopilot.*SessionFile|TestCopilot.*Usage' -count=1
```

Expected: new precedence cases FAIL because `Execute` does not read the file.

- [ ] **Step 7: Integrate the snapshot source**

Capture the resume baseline before process start, read the final snapshot after
`cmd.Wait`, and add a `sessionFileUsage` source to
`copilotEventState.resolveUsage` between complete stdout sources and
`msgUsage`. Log only source/status/model counts, never event lines.

- [ ] **Step 8: Run Copilot tests and verify GREEN**

```bash
cd server
go test ./pkg/agent -run 'TestCopilot' -count=1
go test -race ./pkg/agent -run 'TestCopilot' -count=1
```

Expected: PASS.

- [ ] **Step 9: Commit the collector**

```bash
git add server/pkg/agent/copilot.go server/pkg/agent/copilot_test.go \
  server/pkg/agent/copilot_session_usage.go server/pkg/agent/copilot_session_usage_test.go
git commit -m "fix(agent): recover Copilot usage from session snapshots"
```

### Task 2: Derive runtime usage coverage

**Files:**
- Modify: `server/pkg/db/queries/runtime_usage.sql`
- Modify: `server/pkg/db/generated/runtime_usage.sql.go`
- Modify: `server/internal/handler/runtime.go`
- Modify: `server/cmd/server/router.go`
- Create: `server/internal/handler/runtime_usage_coverage_test.go`

- [ ] **Step 1: Write the failing handler/query test**

Insert completed tasks for one runtime representing:

- input/cache-bearing complete usage;
- output-only usage;
- no usage;
- failed and cancelled tasks that must be excluded.

Request coverage in a timezone that crosses UTC midnight and assert the daily
counts.

- [ ] **Step 2: Run the handler test and verify RED**

```bash
cd server
go test ./internal/handler -run 'TestGetRuntimeUsageCoverage' -count=1 -v
```

Expected: FAIL because the route/query do not exist. Confirm the package did not
print its database-unavailable skip message.

- [ ] **Step 3: Add the sqlc query**

Add `ListRuntimeUsageCoverage` using completed queue rows left-joined to
`task_usage`. Aggregate token totals per task first, then classify each task
and group by `DATE(completed_at AT TIME ZONE @tz)`.

- [ ] **Step 4: Regenerate sqlc**

```bash
make sqlc
```

Expected: `server/pkg/db/generated/runtime_usage.sql.go` contains the new
parameter and row types without unrelated generated drift.

- [ ] **Step 5: Add handler and route**

Return:

```go
type RuntimeUsageCoverageResponse struct {
    Date           string `json:"date"`
    CompletedRuns  int64  `json:"completed_runs"`
    CompleteRuns   int64  `json:"complete_runs"`
    OutputOnlyRuns int64  `json:"output_only_runs"`
    MissingRuns    int64  `json:"missing_runs"`
}
```

Wire `GET /api/runtimes/:runtimeId/usage/coverage` through the same runtime
read-access and viewing-timezone rules as `/usage`.

- [ ] **Step 6: Run handler/query verification**

```bash
cd server
go test ./internal/handler -run 'TestGetRuntimeUsageCoverage|TestGetRuntimeUsage' -count=1 -v
go test ./pkg/db/... -count=1
```

Expected: PASS with real handler assertions.

- [ ] **Step 7: Commit coverage backend**

```bash
git add server/pkg/db/queries/runtime_usage.sql \
  server/pkg/db/generated/runtime_usage.sql.go \
  server/internal/handler/runtime.go \
  server/internal/handler/runtime_usage_coverage_test.go \
  server/cmd/server/router.go
git commit -m "feat(usage): expose runtime telemetry coverage"
```

### Task 3: Add compatible frontend coverage contracts

**Files:**
- Modify: `packages/core/types/agent.ts`
- Modify: `packages/core/api/schemas.ts`
- Modify: `packages/core/api/schemas.test.ts`
- Modify: `packages/core/api/client.ts`
- Modify: `packages/core/runtimes/queries.ts`

- [ ] **Step 1: Write failing schema and query tests**

Assert valid coverage rows parse, malformed/missing numeric fields default to
zero, and the coverage query key contains runtime ID, days, and timezone.

- [ ] **Step 2: Run core tests and verify RED**

```bash
cd packages/core
pnpm exec vitest run api/schemas.test.ts runtimes/queries.test.ts
```

Expected: new tests FAIL because the type/schema/client/query are absent.

- [ ] **Step 3: Implement the contract**

Add `RuntimeUsageCoverage`, a Zod fallback schema, an API client method, and
`runtimeUsageCoverageOptions(runtimeId, days, tz)`. Preserve the existing
usage API shape.

- [ ] **Step 4: Run core tests and typecheck**

```bash
cd packages/core
pnpm exec vitest run api/schemas.test.ts runtimes/queries.test.ts
pnpm typecheck
```

Expected: PASS.

- [ ] **Step 5: Commit frontend contract**

```bash
git add packages/core/types/agent.ts packages/core/api/schemas.ts \
  packages/core/api/schemas.test.ts packages/core/api/client.ts \
  packages/core/runtimes/queries.ts packages/core/runtimes/queries.test.ts
git commit -m "feat(core): add runtime usage coverage query"
```

### Task 4: Render incomplete Copilot telemetry honestly

**Files:**
- Modify: `packages/views/runtimes/utils.ts`
- Modify: `packages/views/runtimes/utils.test.ts`
- Modify: `packages/views/runtimes/components/usage-section.tsx`
- Modify: `packages/views/runtimes/components/usage-section.test.tsx`
- Modify: `packages/views/locales/en/runtimes.json`
- Modify: `packages/views/locales/zh-Hans/runtimes.json`
- Modify: `packages/views/locales/ja/runtimes.json`
- Modify: `packages/views/locales/ko/runtimes.json`

- [ ] **Step 1: Write failing coverage aggregation tests**

Add a pure helper that slices coverage by the same period/timezone boundary and
sums complete/output-only/missing runs. Test a complete period, output-only
period, missing-only period, and prior-window exclusion.

- [ ] **Step 2: Write failing component tests**

Assert:

- complete coverage preserves the current Cost/Input UI;
- output-only coverage shows a lower-bound marker and incomplete-input hint;
- missing coverage reports the missing-run count;
- missing-only coverage does not fall through to the generic no-usage page;
- saved custom prices outside the selected rows are described as inactive.

- [ ] **Step 3: Run views tests and verify RED**

```bash
cd packages/views
pnpm exec vitest run runtimes/utils.test.ts runtimes/components/usage-section.test.tsx
```

Expected: new coverage cases FAIL while the existing 112 tests remain green.

- [ ] **Step 4: Implement coverage UI and i18n**

Fetch coverage with the same 180-day/tz axis, aggregate the selected window,
render an alert above the KPI grid, prefix incomplete cost with `≥`, and
replace the zero-like input hint with an incomplete-data message. Keep charts
based on recorded usage and never synthesize missing token values.

Update all four runtime locale files with equivalent product copy.

- [ ] **Step 5: Correct custom-price notice activation**

Add a pure helper that reports saved overrides as active only when a selected
usage row actually resolves through that custom key. Preserve access to edit
inactive saved overrides but do not claim the current period uses them.

- [ ] **Step 6: Run views verification**

```bash
cd packages/views
pnpm exec vitest run runtimes/utils.test.ts runtimes/components/usage-section.test.tsx
pnpm typecheck
pnpm lint
```

Expected: PASS.

- [ ] **Step 7: Commit the UI**

```bash
git add packages/views/runtimes packages/views/locales/*/runtimes.json
git commit -m "fix(usage): mark incomplete runtime cost estimates"
```

### Task 5: Cross-layer verification

**Files:**
- Verify all scoped files from Tasks 1-4.

- [ ] **Step 1: Run focused Go verification**

```bash
cd server
go test ./pkg/agent -run 'TestCopilot' -count=1
go test -race ./pkg/agent -run 'TestCopilot' -count=1
go test ./internal/handler -run 'TestGetRuntimeUsage' -count=1 -v
go vet ./pkg/agent ./internal/handler
```

- [ ] **Step 2: Run focused TypeScript verification**

```bash
cd packages/core
pnpm exec vitest run api/schemas.test.ts runtimes/queries.test.ts
pnpm typecheck

cd ../views
pnpm exec vitest run runtimes/utils.test.ts runtimes/components/usage-section.test.tsx
pnpm typecheck
pnpm lint
```

- [ ] **Step 3: Run repository checks**

```bash
git diff --check origin/main...HEAD
pnpm typecheck
```

Record unrelated baseline failures rather than changing out-of-scope code.

- [ ] **Step 4: Review the exact branch diff**

Check security boundaries, API fallback behavior, resume double-counting,
timezone slicing, and preservation of unrelated files. Address every critical
or high issue before completion.

- [ ] **Step 5: Commit any verification-only fixes**

```bash
git add server/pkg/agent server/pkg/db/queries/runtime_usage.sql \
  server/pkg/db/generated/runtime_usage.sql.go server/internal/handler \
  server/cmd/server/router.go packages/core packages/views/runtimes \
  packages/views/locales/*/runtimes.json
git commit -m "test(usage): harden Copilot telemetry recovery"
```

Skip this commit when verification requires no code changes.
