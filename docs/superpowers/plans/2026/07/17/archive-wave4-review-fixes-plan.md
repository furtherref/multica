# Archive Wave 4 Review Fixes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close the archive pre-cancel TOCTOU window, prevent provider execution when cancellation is visible immediately after `/start`, and make the archive documentation and source maps match the implemented best-effort contract.

**Architecture:** Keep the pre-write task cancellation so an initial cancellation failure can abort the archive request, then add a post-write sweep to catch tasks that became active before the archive commit. In the daemon, synchronously re-read task status immediately after `/start` and before provider setup; retain the asynchronous watcher for later cancellation. No schema or migration changes.

**Tech Stack:** Go, PostgreSQL/pgx, sqlc-generated queries, Vitest, Markdown built-in skill references.

---

### Task 1: Pin and close the archive pre-cancel TOCTOU window

**Files:**
- Modify: `server/internal/handler/issue_archive_guard_test.go`
- Modify: `server/internal/handler/issue.go`

- [x] **Step 1: Add a failing single-issue concurrency regression test**

Create an active issue with an initial running task, lock its issue row from a separate transaction, start the archive handler, wait until the initial task becomes cancelled, insert and start a second task, release the issue-row lock, and assert both the issue is `archive` and the second task is `cancelled`.

- [x] **Step 2: Run the regression test and verify RED**

Run:

```bash
cd server
set -a; source ../.env.worktree; set +a
go test ./internal/handler -run '^TestArchiveCancelsTaskStartedAfterPreWriteSweep$' -count=1 -v
```

Expected: FAIL because the second task remains `running` after archive commits.

- [x] **Step 3: Add the minimal post-write sweep**

After `UpdateIssue` returns an archive transition, call `CancelTasksForIssue` again before returning success. If the post-write sweep fails, finish the already-committed issue side effects (attachment links, `issue:updated`, and batch `updated` accounting), then return 500 with an explicit retry message; batch responses also include `convergence_failed_issue_ids`. This keeps clients synchronized with the committed archive state while reliably surfacing incomplete task convergence.

- [x] **Step 4: Run the regression and archive handler tests and verify GREEN**

```bash
go test ./internal/handler -run 'Test(ArchiveCancelsTaskStartedAfterPreWriteSweep|BatchArchiveCancelsTaskStartedAfterPreWriteSweep|ArchiveCancelsActiveTasks|BatchArchiveCancelsActiveTasks)$' -count=1 -v
```

Expected: all selected tests PASS.

### Task 2: Pin and close the daemon post-start cancellation boundary

**Files:**
- Modify: `server/internal/daemon/workdir_race_test.go`
- Modify: `server/internal/daemon/daemon.go`

- [x] **Step 1: Add a failing runTask ordering test**

Use the existing fake daemon server/backend patterns to make `/start` succeed, make the following task-status read return `cancelled`, and record whether the provider backend runs. Assert the backend is never invoked.

- [x] **Step 2: Run the daemon test and verify RED**

```bash
go test ./internal/daemon -run '^TestRunTask_CancelledAfterStartDoesNotLaunchProvider$' -count=1 -v
```

Expected: FAIL because current `runTask` continues toward provider execution after `/start`.

- [x] **Step 3: Add a synchronous post-start status check**

Immediately after `StartTask` succeeds and before progress/provider setup, call `GetTaskStatus`. If `shouldInterruptAgent` reports terminal/deleted, stop the prepare lease and return a cancelled result without invoking the backend. Keep transient status-read errors best-effort so a temporary network failure does not kill valid work.

- [x] **Step 4: Run the new test and watcher tests and verify GREEN**

```bash
go test ./internal/daemon -run 'Test(RunTask_CancelledAfterStartDoesNotLaunchProvider|WatchTaskCancellation_ImmediateFirstCheck|WatchTaskCancellation_RunningTaskNotInterrupted)$' -count=1 -v
```

Expected: all selected tests PASS.

### Task 3: Repair archive documentation and source-map evidence

**Files:**
- Modify: `docs/superpowers/plans/2026/07/15/archive-consistency-fixes-plan.md`
- Modify: `docs/superpowers/specs/2026/05/07/issue-archive-status-design.md`
- Modify: `server/internal/service/builtin_skills/multica-mentioning/references/mentioning-source-map.md`
- Modify: `server/internal/service/builtin_skills/multica-working-on-issues/references/working-on-issues-source-map.md`

- [x] **Step 1: Replace absolute concurrency claims**

State that enqueue/claim/start guards and double-sweep cancellation provide best-effort convergence, with a synchronous post-start check preventing provider execution when cancellation is already visible. Do not combine `never starts` with an exception saying it can launch.

- [x] **Step 2: Refresh every stale pending-task dedup citation**

Point SQL rows to `server/pkg/db/queries/agent.sql:907-938` and refresh the helper/call-site rows in `server/internal/handler/comment.go` from the current checkout. Recheck archive handler citations after the implementation shifts line numbers.

- [x] **Step 3: Run source-map pattern checks**

```bash
rg -n 'agent\.sql:544|comment\.go:1232|comment\.go:1197|comment\.go:1397|comment\.go:1440' \
  server/internal/service/builtin_skills/multica-mentioning/references/mentioning-source-map.md
rg -n 'never started.*can still launch|never starts.*best-effort' \
  docs/superpowers/specs/2026/05/07/issue-archive-status-design.md \
  docs/superpowers/plans/2026/07/15/archive-consistency-fixes-plan.md
```

Expected: no stale citation or contradictory archive guarantee remains in the modified contract sections.

### Task 4: Full verification and review

**Files:**
- Verify all files changed by Tasks 1-3.

- [x] **Step 1: Run backend verification**

```bash
cd server
set -a; source ../.env.worktree; set +a
go test ./internal/handler ./internal/service ./internal/daemon -count=1
go vet ./internal/handler ./internal/service/... ./internal/daemon ./pkg/db/generated
go build ./...
```

- [x] **Step 2: Run frontend verification**

```bash
cd ..
pnpm --filter @multica/core test
pnpm --filter @multica/views test
pnpm turbo typecheck --filter=!@multica/mobile --force
```

- [x] **Step 3: Verify formatting and worktree scope**

```bash
git diff --check
git diff --name-only -z -- '*.go' | xargs -0 gofmt -l
git status --short --branch
```

Expected: tests, vet, build, typecheck, and formatting pass; only the planned files are modified.
