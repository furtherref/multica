# Archive Consistency Fixes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the fork-only `archive` issue status consistently mean "retired work": normal create/assign/promote/rerun/comment paths do not enqueue archived work; claim/reclaim/retry guards, double-sweep cancellation, and a synchronous post-`/start` status check provide best-effort convergence under concurrency; archived issues do not block new issues as duplicates or hold child-stage barriers open. This is not an absolute zero-model-spend guarantee at the final read-to-provider race; see the design addendum for the precise bound.

**Architecture:** The original implementation contains eight focused Go backend fixes: five guard existing decision points with `archive`, one adds DB-level claim/retry guards, one fixes the batch child-done parent guard, and one prevents GitHub PR webhooks from resurrecting archived issues. The wave-4 follow-up adds handler-side pre/post-write cancellation convergence and a daemon-side synchronous post-`/start` status check. No schema changes, migrations, or new endpoints are required. The wire-visible `issue_archived` reason has matching frontend copy, and verification combines DB-backed handler tests with a daemon provider-boundary regression test plus built-in skill source-map updates.

**Tech Stack:** Go (Chi, sqlc, pgx), PostgreSQL, TypeScript (vitest, i18next locale bundles).

**Revision note (2026-07-16):** Revised after an external review confirmed four gaps in v1 of this plan: (P0) no issue-status check at task claim/retry time leaves concurrency races that create runnable tasks on archived issues; the batch child-done path has its own duplicated parent guard; the GitHub PR-merge webhook auto-advances archived issues to done; several `-run` regexes matched zero tests. All were verified against code before revising.

**Background (verified against code on branch `fix/archive-consistency`, base `7ebcde085`):**

| # | Defect | Evidence |
|---|--------|----------|
| a | Creating an issue directly in `archive` with an agent assignee enqueues a task and never cancels it | `service/issue.go:412-417` skips only `backlog`; no archive-cancel on the create path |
| b | `backlog → archive` enqueues a run, then immediately cancels it (wasted task row + churn) | `service/issue_trigger.go:107-108` excludes only `done`/`cancelled`; cancel at `handler/issue.go:2761` |
| c | Manual rerun and comment triggers (explicit @mentions AND implicit routing) work on archived issues | `handler/task_lifecycle.go:122-181` has no status guard; `handler/comment.go:2182+` documents "no issue status gate here" |
| d | Duplicate detection treats archived issues as active duplicates and blocks re-creating the same title | `pkg/db/queries/issue.sql:130,140`: `status NOT IN ('done', 'cancelled')` |
| e | Stage barriers: an archived child holds its stage open forever; an archived parent is woken by child completions — in BOTH the single path and the batch path's duplicated guard | `handler/issue_child_done.go:340-342` (child side), `:90` (single-path parent side), `:188` (batch-path parent side) |
| f | No issue-status check where tasks become executable: `ClaimAgentTask` (the ONLY `queued → dispatched` transition, `agent.sql:442`) and `CreateRetryTask` (`agent.sql:290+`) ignore issue status. A comment/rerun enqueue racing an archive, or a fail+retry transaction committing after `CancelTasksForIssue` scanned rows, leaves an orphan queued task that WILL be claimed and executed on a retired issue | `agent.sql:431-470` (claim has no issue join); `agent.sql:329-359` (retry clones unconditionally); `service/task.go:2913-2927` (retry inside the fail tx); nothing ever re-cancels stragglers — daemon GC (`daemon/gc.go:308`) only cleans local task directories |
| g | GitHub PR-merge webhook resurrects archived issues: the auto-done re-eval skips only `done`/`cancelled`, so a merged close-intent PR flips `archive → done` | `handler/github.go:975-985` (`advanceIssueToDone`) |

Already consistent (verified, no change needed): daemon GC treats archive as terminal (`internal/daemon/gc.go:308-315`); issue list/count filters include archive (`handler/issue.go:478`); inbox cleanup includes archive (`pkg/db/queries/inbox.sql:90`, `ArchiveCompletedInbox`); `FailAgentTask` requires `status IN ('dispatched','running','waiting_local_directory')` (`agent.sql:688`), so an already-cancelled task can never be failed into a retry.

## Global Constraints

- `archive` is the fork-original status added by migration `069_issue_archive_status` / PR #39 — the string is **`archive`** (singular), never `archived`. Upstream has no such status; keep fork-only logic clearly commented as such (existing convention: "fork status #39").
- No DB foreign keys, no cascades, no new migrations are needed anywhere in this plan. If you find yourself writing a migration, stop — you've gone off plan. (SQL query changes + `sqlc` regeneration are expected; schema changes are not.)
- Code comments must be English. Conventional commit prefixes (`fix(scope)`, `test(scope)`).
- Handler/service Go tests need a reachable PostgreSQL; they **silently skip** (exit 0, "Skipping tests: could not connect to database") when `DATABASE_URL` is unreachable. After every test run, confirm the output shows real PASS/FAIL lines, not the skip message.
- Do NOT run `make check` for verification — its Go step has known environmental failures in this sandbox (config tests poisoned by `.env`, pg_cron flakes, agent-CLI tests). Use the targeted package tests listed in each task. No coverage-percentage gate applies to this work (the 80% figure in the ROI design doc is that feature's own target, not a repo rule), and no Playwright E2E is added: these are server-side guards fully exercised by the DB-backed API-level Go tests.
- The repo `Makefile` lives at the REPO ROOT only — there is no `server/Makefile`. All `make` invocations below use `make -C <repo-root>` explicitly since the test steps leave the shell in `server/`.
- `make sqlc` requires sqlc **v1.31.1** (repo pin). Local v1.29.0 silently downgrades the generated tree. Verify `sqlc version` before regenerating; install with `go install github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1` if needed.
- i18n: adding an EN locale key requires the same key in `zh-Hans`, `ja`, and `ko` bundles, or `packages/views/locales/parity.test.ts` fails.
- Server-driven enums rendered by the frontend must keep a `default` branch (repo API-compatibility rule) — the new reason code is additive.
- CLAUDE.md rule: product behavior documented by built-in skills under `server/internal/service/builtin_skills/*` must have its `SKILL.md` and `references/*-source-map.md` updated in the same PR. The comment-trigger change touches `multica-mentioning` (Task 5).

## One-Time Environment Setup

- [ ] **Setup 1: Worktree DB.** From the worktree root (`.worktrees/archive-consistency/`): run `make worktree-env && make setup-worktree` to provision the isolated per-worktree database (worktrees share one PostgreSQL container; `.env.worktree` carries the isolated `DATABASE_URL`). If the shared container is not running, `make dev` in the main checkout starts it.
- [ ] **Setup 2: Export DATABASE_URL for go test.** All `go test` commands below assume, from `server/`:

```bash
set -a; source ../.env.worktree; set +a   # exports DATABASE_URL et al.
```

- [ ] **Setup 3: Sanity run.** `go test ./internal/handler/ -run TestChildDoneNotifiesParent -v` → expected: `PASS` (not "Skipping tests").

---

### Task 1: Enqueue predicates treat `archive` as never-run (fixes a + b)

**Files:**
- Modify: `server/internal/service/issue.go:407-435` (`shouldEnqueueAgentTask`, `shouldEnqueueSquadLeaderOnAssign`)
- Modify: `server/internal/handler/issue.go:2850-2860` (`shouldEnqueueAgentTask` handler mirror)
- Modify: `server/internal/service/issue_trigger.go:100-115` (`WillEnqueueRun` assign + status branches)
- Test: `server/internal/handler/issue_archive_guard_test.go` (new file)

**Interfaces:**
- Consumes: existing test fixtures from package `handler`: `createHandlerTestAgent(t, name, mcpConfig)` (`handler_test.go:216`), `updateChildStatus(t, issueID, status)` (`issue_child_done_test.go:64`), `newRequest`, `withURLParam`, `testHandler`, `testPool`, `testWorkspaceID`, `IssueResponse`.
- Produces: test helpers `taskCountForIssue(t, issueID string) int` and `createIssueViaHTTP(t, body map[string]any) IssueResponse` — reused verbatim by Tasks 2–7 (same package).

Coverage note: the archive check in `WillEnqueueRun` sits BEFORE the agent/squad switch, so one guard line covers both assignee types on the update/batch paths; `shouldEnqueueSquadLeaderOnAssign` receives the identical two-token change adjacent to the agent variant tested below. The batch endpoint is exercised explicitly because it is a distinct HTTP entry point.

- [ ] **Step 1: Write the failing tests**

Create `server/internal/handler/issue_archive_guard_test.go`:

```go
package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// taskCountForIssue returns how many agent_task_queue rows (ANY status) exist
// for the issue. The archive guards must leave ZERO rows — an
// enqueue-then-cancel still counts as a regression, so counting only
// non-cancelled rows would be too weak an assertion.
func taskCountForIssue(t *testing.T, issueID string) int {
	t.Helper()
	var n int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM agent_task_queue WHERE issue_id = $1`, issueID,
	).Scan(&n); err != nil {
		t.Fatalf("count tasks for issue: %v", err)
	}
	return n
}

// createIssueViaHTTP drives the real CreateIssue handler and returns the
// created issue. Cleanup is registered on the test.
func createIssueViaHTTP(t *testing.T, body map[string]any) IssueResponse {
	t.Helper()
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, body)
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var issue IssueResponse
	if err := json.NewDecoder(w.Body).Decode(&issue); err != nil {
		t.Fatalf("decode issue: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, issue.ID)
	})
	return issue
}

// Fix (a): an issue born in archive is retired on arrival — assigning an
// agent to it must not start a run.
func TestCreateIssueInArchiveDoesNotEnqueue(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := createHandlerTestAgent(t, "ArchiveCreateNoEnqueue", []byte("[]"))
	issue := createIssueViaHTTP(t, map[string]any{
		"title":         "archive-create-no-enqueue",
		"status":        "archive",
		"assignee_type": "agent",
		"assignee_id":   agentID,
	})
	if got := taskCountForIssue(t, issue.ID); got != 0 {
		t.Fatalf("creating an issue directly in archive must not enqueue; got %d task row(s)", got)
	}
}

// Fix (b): backlog -> archive must not enqueue-then-cancel. Zero task rows,
// not "one cancelled row".
func TestBacklogToArchiveDoesNotEnqueue(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := createHandlerTestAgent(t, "ArchiveFromBacklog", []byte("[]"))
	issue := createIssueViaHTTP(t, map[string]any{
		"title":         "archive-from-backlog-no-enqueue",
		"status":        "backlog",
		"assignee_type": "agent",
		"assignee_id":   agentID,
	})
	if got := taskCountForIssue(t, issue.ID); got != 0 {
		t.Fatalf("backlog parking lot must not enqueue on create; got %d", got)
	}
	updateChildStatus(t, issue.ID, "archive")
	if got := taskCountForIssue(t, issue.ID); got != 0 {
		t.Fatalf("backlog->archive must not enqueue (not even enqueue-then-cancel); got %d task row(s)", got)
	}
}

// Fix (b), batch entry point: same rule through BatchUpdateIssues, which is a
// distinct HTTP path sharing WillEnqueueRun.
func TestBatchBacklogToArchiveDoesNotEnqueue(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := createHandlerTestAgent(t, "ArchiveBatchFromBacklog", []byte("[]"))
	issue := createIssueViaHTTP(t, map[string]any{
		"title":         "archive-batch-from-backlog",
		"status":        "backlog",
		"assignee_type": "agent",
		"assignee_id":   agentID,
	})
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues/batch-update", map[string]any{
		"issue_ids": []string{issue.ID},
		"updates":   map[string]any{"status": "archive"},
	})
	testHandler.BatchUpdateIssues(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("batch update: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := taskCountForIssue(t, issue.ID); got != 0 {
		t.Fatalf("batch backlog->archive must not enqueue; got %d task row(s)", got)
	}
}

// Fix (a), assign branch: assigning an agent onto an ALREADY archived issue
// must not start a run either.
func TestAssignAgentOnArchivedIssueDoesNotEnqueue(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := createHandlerTestAgent(t, "ArchiveAssignNoEnqueue", []byte("[]"))
	issue := createIssueViaHTTP(t, map[string]any{
		"title":  "archive-assign-no-enqueue",
		"status": "todo",
	})
	updateChildStatus(t, issue.ID, "archive")
	w := httptest.NewRecorder()
	req := newRequest("PUT", "/api/issues/"+issue.ID, map[string]any{
		"assignee_type": "agent",
		"assignee_id":   agentID,
	})
	req = withURLParam(req, "id", issue.ID)
	testHandler.UpdateIssue(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateIssue assignee: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := taskCountForIssue(t, issue.ID); got != 0 {
		t.Fatalf("assigning on an archived issue must not enqueue; got %d task row(s)", got)
	}
}
```

The `errors`, `pgx`, `db`, and `strings` imports serve Tasks 2 and 4's tests in this same file; if the compiler flags any as unused at this step, drop it now and re-add it in the task that needs it.

- [ ] **Step 2: Run tests to verify they fail**

Run (from `server/`, env sourced per Setup 2):

```bash
go test ./internal/handler/ -run 'TestCreateIssueInArchiveDoesNotEnqueue|TestBacklogToArchiveDoesNotEnqueue|TestBatchBacklogToArchiveDoesNotEnqueue|TestAssignAgentOnArchivedIssueDoesNotEnqueue' -v
```

Expected: all four FAIL with "got 1 task row(s)" (and confirm the output is not "Skipping tests").

- [ ] **Step 3: Implement the guards**

In `server/internal/service/issue.go`, replace the two predicates (currently at :407-435):

```go
// shouldEnqueueAgentTask returns true when an issue create or assignment
// should trigger the assigned agent. Backlog issues are skipped — backlog
// acts as a parking lot for pre-assigning without immediate execution.
// Archive (fork status #39) is retired work: assigning into it must never
// start a run either. Mirrors handler.shouldEnqueueAgentTask; kept here to
// make the service self-contained, since both code paths must move together.
func (s *IssueService) shouldEnqueueAgentTask(ctx context.Context, issue db.Issue) bool {
	if issue.Status == "backlog" || issue.Status == "archive" {
		return false
	}
	return s.isAgentAssigneeReady(ctx, issue)
}
```

```go
func (s *IssueService) shouldEnqueueSquadLeaderOnAssign(ctx context.Context, issue db.Issue) bool {
	if issue.Status == "backlog" || issue.Status == "archive" {
		return false
	}
	return s.isSquadLeaderReady(ctx, issue)
}
```

In `server/internal/handler/issue.go` (:2850-2860), mirror the same change:

```go
// shouldEnqueueAgentTask returns true when an issue creation or assignment
// should trigger the assigned agent. Backlog issues are skipped — backlog
// acts as a parking lot where issues can be pre-assigned without immediately
// triggering execution; moving out of backlog is handled separately in
// UpdateIssue. Archive (fork status #39) is retired work and never enqueues.
func (h *Handler) shouldEnqueueAgentTask(ctx context.Context, issue db.Issue) bool {
	if issue.Status == "backlog" || issue.Status == "archive" {
		return false
	}
	return h.isAgentAssigneeReady(ctx, issue)
}
```

In `server/internal/service/issue_trigger.go`, `WillEnqueueRun` (:100-115), change both branches:

```go
	var source RunEnqueueSource
	switch {
	case in.IsCreate || in.AssigneeChanged:
		// Backlog is the parking lot: assigning into backlog never starts a
		// run. Archive (fork status #39) is retired work: assigning into it
		// never starts a run either.
		if issue.Status == "backlog" || issue.Status == "archive" {
			return IssueRunTrigger{}, false
		}
		source = RunSourceAssign
	case in.StatusChanged && in.PrevStatus == "backlog" &&
		issue.Status != "done" && issue.Status != "cancelled" && issue.Status != "archive":
		if probe.IsSelfLoop != nil && probe.IsSelfLoop() {
			return IssueRunTrigger{}, false
		}
		source = RunSourceStatus
	default:
		return IssueRunTrigger{}, false
	}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/handler/ -run 'TestCreateIssueInArchiveDoesNotEnqueue|TestBacklogToArchiveDoesNotEnqueue|TestBatchBacklogToArchiveDoesNotEnqueue|TestAssignAgentOnArchivedIssueDoesNotEnqueue' -v
```

Expected: 4× PASS.

- [ ] **Step 5: Regression check on neighboring behavior**

The enqueue predicate is shared with the trigger-preview endpoint (its tests are named `TestPreviewIssueTrigger_*` — NOT "TestIssueTriggerPreview") and the reassign/no-cancel rules:

```bash
go test ./internal/handler/ -run 'TestPreviewIssueTrigger|TestUpdateIssueReassign' -v
```

Expected: PASS, with a non-zero test count in the output (a regex that matches zero tests exits 0 and proves nothing — check that test names are actually listed).

- [ ] **Step 6: Commit**

```bash
git add server/internal/service/issue.go server/internal/service/issue_trigger.go server/internal/handler/issue.go server/internal/handler/issue_archive_guard_test.go
git commit -m "fix(issues): never enqueue agent runs into or onto archived issues"
```

---

### Task 2: DB-level execution guards — claim + retry never run archived work (fix f, P0)

Handler-entry guards (Tasks 1, 4, 5) read the issue BEFORE inserting, so a concurrent archive can still slip a task in: request A loads an active issue; request B archives and `CancelTasksForIssue` scans; request A's insert (or a fail+retry transaction) commits after the scan — an orphan queued task on an archived issue that nothing ever cancels. `ClaimAgentTask` is the ONLY `queued → dispatched` transition in the codebase (`agent.sql:442` is the sole `SET status = 'dispatched'`), so a status predicate there makes every such orphan permanently inert, and a predicate on `CreateRetryTask` stops the biggest systematic orphan source at creation. This achieves the invariant ("a task on an archived issue never executes") without the cross-path transactions/row locks a heavier design would need.

**Files:**
- Modify: `server/pkg/db/queries/agent.sql` (`ClaimAgentTask` :437-470, `CreateRetryTask` :290-359)
- Modify: `server/internal/service/task.go:2913-2927` (tolerate suppressed retry)
- Regenerate: `server/pkg/db/generated/agent.sql.go` (via sqlc; do not hand-edit)
- Test: `server/internal/handler/issue_archive_guard_test.go` (append)

**Interfaces:**
- Consumes: `insertRunningIssueTask(t, agentID, issueID)` (`issue_reassign_no_cancel_test.go:12`), fixtures from Task 1; `db.New(testPool)` for direct query calls; `parseUUID` (handler package, trusted fixture round-trip).
- Produces: `ClaimAgentTask` returns `pgx.ErrNoRows` instead of a task when the only queued work belongs to archived issues; `CreateRetryTask` returns `pgx.ErrNoRows` when the parent's issue is archived; `FailTask` treats that as "retry suppressed", not an error. Test helper `insertQueuedIssueTask(t, agentID, issueID string) string`.

- [ ] **Step 1: Write the failing tests**

Append to `server/internal/handler/issue_archive_guard_test.go`:

```go
// insertQueuedIssueTask seeds a queued task for the agent on the issue,
// mirroring insertRunningIssueTask but in 'queued' state so ClaimAgentTask
// can see it.
func insertQueuedIssueTask(t *testing.T, agentID, issueID string) string {
	t.Helper()
	var taskID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO agent_task_queue (agent_id, runtime_id, status, priority, issue_id)
		VALUES ($1, (SELECT runtime_id FROM agent WHERE id = $1), 'queued', 0, $2)
		RETURNING id::text
	`, agentID, issueID).Scan(&taskID); err != nil {
		t.Fatalf("insert queued task: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE id = $1`, taskID) })
	return taskID
}

// Fix (f), claim side: a queued task whose issue is archived must never be
// claimed. The direct SQL archive (bypassing the handler's cancel) simulates
// the race orphan: an insert that committed after CancelTasksForIssue scanned.
func TestClaimSkipsTasksOnArchivedIssue(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := createHandlerTestAgent(t, "ArchiveClaimGuard", []byte("[]"))
	issueID := insertAgentAssignedIssue(t, agentID, 92165, "claim-archived-guard")
	taskID := insertQueuedIssueTask(t, agentID, issueID)

	ctx := context.Background()
	if _, err := testPool.Exec(ctx, `UPDATE issue SET status = 'archive' WHERE id = $1`, issueID); err != nil {
		t.Fatalf("archive issue: %v", err)
	}
	q := db.New(testPool)
	if _, err := q.ClaimAgentTask(ctx, db.ClaimAgentTaskParams{
		AgentID:          parseUUID(agentID),
		PrepareLeaseSecs: 60,
	}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("claim must skip the archived issue's task, got err=%v", err)
	}

	// Restoring the issue makes the same task claimable again.
	if _, err := testPool.Exec(ctx, `UPDATE issue SET status = 'todo' WHERE id = $1`, issueID); err != nil {
		t.Fatalf("restore issue: %v", err)
	}
	claimed, err := q.ClaimAgentTask(ctx, db.ClaimAgentTaskParams{
		AgentID:          parseUUID(agentID),
		PrepareLeaseSecs: 60,
	})
	if err != nil {
		t.Fatalf("claim after restore: %v", err)
	}
	if uuidToString(claimed.ID) != taskID {
		t.Fatalf("expected to claim %s after restore, got %s", taskID, uuidToString(claimed.ID))
	}
}

// Fix (f), retry side: the fail+retry transaction must not clone a new
// attempt for an issue that was archived while the parent ran.
func TestRetryNotCreatedForArchivedIssue(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := createHandlerTestAgent(t, "ArchiveRetryGuard", []byte("[]"))
	issueID := insertAgentAssignedIssue(t, agentID, 92166, "retry-archived-guard")
	taskID := insertRunningIssueTask(t, agentID, issueID)

	ctx := context.Background()
	if _, err := testPool.Exec(ctx, `UPDATE issue SET status = 'archive' WHERE id = $1`, issueID); err != nil {
		t.Fatalf("archive issue: %v", err)
	}
	q := db.New(testPool)
	if _, err := q.CreateRetryTask(ctx, db.CreateRetryTaskParams{
		ID: parseUUID(taskID),
	}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("retry clone must be suppressed on an archived issue, got err=%v", err)
	}
	if got := taskCountForIssue(t, issueID); got != 1 {
		t.Fatalf("expected only the original task row, got %d", got)
	}
}
```

If the generated param struct field names differ (check `server/pkg/db/generated/agent.sql.go` after Step 4), use the generated names — do not hand-edit generated code.

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/handler/ -run 'TestClaimSkipsTasksOnArchivedIssue|TestRetryNotCreatedForArchivedIssue' -v
```

Expected: both FAIL — the claim returns the archived issue's task; the retry clone succeeds.

- [ ] **Step 3: Add the SQL predicates**

In `server/pkg/db/queries/agent.sql`, `ClaimAgentTask` (:437-470): inside the inner `SELECT`, directly after `WHERE atq.agent_id = $1 AND atq.status = 'queued'`, add:

```sql
      -- Archive (fork status #39) is retired work: a task whose issue was
      -- archived after enqueue (insert/retry racing the archive cancel) must
      -- never be claimed. This is the single queued->dispatched transition,
      -- so the predicate makes every such orphan permanently inert.
      AND (atq.issue_id IS NULL OR NOT EXISTS (
          SELECT 1 FROM issue i WHERE i.id = atq.issue_id AND i.status = 'archive'
      ))
```

In `CreateRetryTask` (:357-359), widen the final `WHERE`:

```sql
FROM agent_task_queue p
WHERE p.id = $1
  -- Archive (fork status #39): no retry attempt is raised on retired work.
  -- Callers treat the resulting no-row as "retry suppressed", not an error.
  AND (p.issue_id IS NULL OR NOT EXISTS (
      SELECT 1 FROM issue i WHERE i.id = p.issue_id AND i.status = 'archive'
  ))
RETURNING *;
```

- [ ] **Step 4: Regenerate sqlc**

```bash
sqlc version   # MUST print v1.31.1 — if not: go install github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1
make -C .. sqlc
git diff --stat
```

Expected diff: only `agent.sql` and `pkg/db/generated/agent.sql.go`. A wide generated diff means the wrong sqlc version — `git checkout -- server/pkg/db/generated/` and fix the version first.

- [ ] **Step 5: Tolerate suppressed retries in the fail transaction**

In `server/internal/service/task.go` (:2916-2927), the fail transaction currently treats ANY `CreateRetryTask` error as fatal, which after Step 3 would roll back the fail itself. Replace the retry block:

```go
		if wantRetry {
			child, cerr := qtx.CreateRetryTask(ctx, db.CreateRetryTaskParams{
				ID:                   taskID,
				RuntimeMcpOverlay:    retryOverlay.Overlay,
				RuntimeConnectedApps: retryOverlay.ConnectedApps,
			})
			switch {
			case errors.Is(cerr, pgx.ErrNoRows):
				// The issue was archived while this task ran (fork status
				// #39): the fail stands, but no retry is raised on retired
				// work — CreateRetryTask's WHERE suppressed the clone.
				slog.Info("retry suppressed: issue archived",
					"task_id", util.UUIDToString(taskID))
			case cerr != nil:
				return fmt.Errorf("create retry task: %w", cerr)
			default:
				retried = &child
			}
		}
```

(`errors` and `pgx` are already imported in task.go — see :2930.)

- [ ] **Step 6: Run tests to verify they pass**

```bash
go test ./internal/handler/ -run 'TestClaimSkipsTasksOnArchivedIssue|TestRetryNotCreatedForArchivedIssue' -v
go test ./internal/service/
go build ./...
```

Expected: both new tests PASS; the full service package (fail/retry/claim machinery lives there) still PASSes; clean build.

- [ ] **Step 7: Commit**

```bash
git add server/pkg/db/queries/agent.sql server/pkg/db/generated/agent.sql.go server/internal/service/task.go server/internal/handler/issue_archive_guard_test.go
git commit -m "fix(tasks): never claim or retry tasks on archived issues"
```

---

### Task 3: Duplicate detection excludes archived issues (fix d)

**Files:**
- Modify: `server/pkg/db/queries/issue.sql:130` (`FindActiveDuplicateIssue`) and `:140` (`FindRecentAutopilotDuplicateIssue`)
- Regenerate: `server/pkg/db/generated/issue.sql.go` (via sqlc; do not hand-edit)
- Test: `server/internal/handler/issue_archive_guard_test.go` (append)

**Interfaces:**
- Consumes: `createIssueViaHTTP`, `updateChildStatus` from Task 1; `issueguard.LockAndFindRecentAutopilotDuplicate` (`internal/issueguard/duplicate.go:79`).
- Produces: nothing new for later tasks.

- [ ] **Step 1: Write the failing tests**

Append to `server/internal/handler/issue_archive_guard_test.go`:

```go
// Fix (d): an archived issue is retired — it must not block re-creating the
// same title as an "active duplicate". (Duplicate detection runs in
// service.CreateIssue via issueguard.LockAndFindActiveDuplicate; done and
// cancelled are already excluded.)
func TestArchivedIssueIsNotADuplicateBlocker(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	title := "archive-dup-title-guard"
	first := createIssueViaHTTP(t, map[string]any{"title": title, "status": "todo"})
	updateChildStatus(t, first.ID, "archive")
	second := createIssueViaHTTP(t, map[string]any{"title": title, "status": "todo"})
	if second.ID == first.ID {
		t.Fatalf("expected a fresh issue, got the archived one back")
	}
}
```

Also add the autopilot-variant test (the second query changed in this task). It calls `issueguard.LockAndFindRecentAutopilotDuplicate` directly; seed the archived autopilot-origin issue plus its matching `autopilot_run` row by copying the `INSERT INTO autopilot_run` fixture used in `internal/handler/autopilot_list_test.go` (adjust only issue linkage and timestamps):

```go
// Fix (d), autopilot variant: an archived autopilot-created issue must not
// suppress the autopilot's next run within the dedup window.
func TestArchivedAutopilotIssueIsNotADuplicateBlocker(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	// Seed an archived issue with origin_type='autopilot' and a matching
	// autopilot_run row (fixture copied from autopilot_list_test.go), then:
	found, ok, err := issueguard.LockAndFindRecentAutopilotDuplicate(
		context.Background(), db.New(testPool),
		parseUUID(testWorkspaceID), autopilotID, pgtype.UUID{}, seededTitle, time.Hour,
	)
	if err != nil {
		t.Fatalf("autopilot duplicate lookup: %v", err)
	}
	if ok {
		t.Fatalf("archived autopilot issue must not count as a duplicate, found %s", uuidToString(found.ID))
	}
}
```

(The fixture seeding block is the only part copied from `autopilot_list_test.go` — everything else above is complete. Wrap the lock call in a transaction if `LockIssueDuplicateKey` requires one — mirror how `autopilot.go:590` calls it.)

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/handler/ -run 'TestArchivedIssueIsNotADuplicateBlocker|TestArchivedAutopilotIssueIsNotADuplicateBlocker' -v
```

Expected: FAIL — the second `createIssueViaHTTP` gets a duplicate rejection ("Active duplicate issue exists ... (status: archive)"), and the autopilot lookup returns `ok == true`.

- [ ] **Step 3: Fix the two queries**

In `server/pkg/db/queries/issue.sql`, change both WHERE clauses. Line 130:

```sql
-- name: FindActiveDuplicateIssue :one
SELECT * FROM issue
WHERE workspace_id = $1
  AND status NOT IN ('done', 'cancelled', 'archive')
```

Line 140 (autopilot variant):

```sql
-- name: FindRecentAutopilotDuplicateIssue :one
SELECT i.* FROM issue i
WHERE i.workspace_id = $1
  AND i.status NOT IN ('done', 'cancelled', 'archive')
```

(Only the `NOT IN` lists change; every other line of both queries stays as-is.)

- [ ] **Step 4: Regenerate sqlc**

```bash
sqlc version   # MUST print v1.31.1
make -C .. sqlc
git diff --stat
```

Expected diff: only `issue.sql` and `pkg/db/generated/issue.sql.go`.

- [ ] **Step 5: Run tests to verify they pass**

```bash
go test ./internal/handler/ -run 'TestArchivedIssueIsNotADuplicateBlocker|TestArchivedAutopilotIssueIsNotADuplicateBlocker' -v
go build ./...
```

Expected: PASS, clean build.

- [ ] **Step 6: Commit**

```bash
git add server/pkg/db/queries/issue.sql server/pkg/db/generated/issue.sql.go server/internal/handler/issue_archive_guard_test.go
git commit -m "fix(issues): exclude archived issues from duplicate detection"
```

---

### Task 4: `issue_archived` dispatch reason + rerun guard (fix c, rerun half)

**Files:**
- Modify: `server/internal/dispatch/reason.go` (new reason code)
- Modify: `server/internal/handler/admission.go:52-64` (re-export const block) and `:110+` (`dispatchBlockedFallbackMessage`)
- Modify: `server/internal/handler/task_lifecycle.go:122-128` (`RerunIssue` guard)
- Test: `server/internal/handler/issue_archive_guard_test.go` (append)

**Interfaces:**
- Consumes: `createHandlerTestAgent`, `insertAgentAssignedIssue(t, agentID, number, title)` (`issue_reassign_no_cancel_test.go:28`), `updateChildStatus`.
- Produces: `dispatch.ReasonIssueArchived` / handler alias `ReasonIssueArchived` (`ReasonCode` value `"issue_archived"`) — consumed by Task 5 and by the frontend copy in Task 5. HTTP contract: rerun on an archived issue → `409` with body `{"error": "...", "reason_code": "issue_archived"}`.

- [ ] **Step 1: Write the failing test**

Append to `server/internal/handler/issue_archive_guard_test.go`:

```go
// Fix (c), rerun half: a manual rerun must not raise new agent spend on
// retired work. 409 + machine-readable reason, so the CLI and UI can say why.
func TestRerunBlockedOnArchivedIssue(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := createHandlerTestAgent(t, "ArchiveRerunBlocked", []byte("[]"))
	issueID := insertAgentAssignedIssue(t, agentID, 92162, "rerun-archived-blocked")
	updateChildStatus(t, issueID, "archive")

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues/"+issueID+"/rerun", map[string]any{})
	req = withURLParam(req, "id", issueID)
	testHandler.RerunIssue(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("rerun on archived issue: expected 409, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "issue_archived") {
		t.Fatalf("expected reason_code issue_archived in body, got: %s", w.Body.String())
	}
	if got := taskCountForIssue(t, issueID); got != 0 {
		t.Fatalf("blocked rerun must not create a task; got %d", got)
	}
}
```

If a hardcoded issue `number` (92162/92163/…) collides with an existing test, pick the next free 921xx number.

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/handler/ -run TestRerunBlockedOnArchivedIssue -v
```

Expected: FAIL — currently 202 Accepted (a task is created on the archived issue).

- [ ] **Step 3: Add the reason code**

In `server/internal/dispatch/reason.go`, append inside the existing `const` block (after `ReasonSelfTriggerSuppressed`):

```go
	// ReasonIssueArchived: the target issue is archived (fork status #39) —
	// retired work refuses new runs until the issue is restored. Reveals
	// nothing about any target: the caller can already see the issue.
	ReasonIssueArchived ReasonCode = "issue_archived"
```

In `server/internal/handler/admission.go`, add to the re-export const block (after `ReasonSelfTriggerSuppressed`):

```go
	ReasonIssueArchived         = dispatch.ReasonIssueArchived
```

In `dispatchBlockedFallbackMessage` (same file, :110+), add a case before `default`:

```go
	case ReasonIssueArchived:
		return "this issue is archived; restore it before running agents"
```

- [ ] **Step 4: Add the rerun guard**

In `server/internal/handler/task_lifecycle.go`, `RerunIssue`, immediately after the `loadIssueForUser` block (:124-127):

```go
	// Archive (fork status #39) retires the issue: a manual rerun must not
	// raise new agent spend on retired work. Restore the issue first.
	if issue.Status == "archive" {
		h.writeDispatchBlocked(w, http.StatusConflict, ReasonIssueArchived)
		return
	}
```

- [ ] **Step 5: Run tests to verify they pass**

```bash
go test ./internal/handler/ -run 'TestRerunBlockedOnArchivedIssue|TestRerunIssue' -v
```

Expected: the new test PASSes and the pre-existing `TestRerunIssue_PrivateHistoricalAgent` still PASSes (non-zero test count).

- [ ] **Step 6: Commit**

```bash
git add server/internal/dispatch/reason.go server/internal/handler/admission.go server/internal/handler/task_lifecycle.go server/internal/handler/issue_archive_guard_test.go
git commit -m "fix(issues): block manual rerun on archived issues with issue_archived reason"
```

---

### Task 5: Comment triggers refuse archived issues (fix c, mention half) + frontend copy + skill docs

**Files:**
- Modify: `server/internal/handler/comment.go:1874-1890` (`computeCommentAgentTriggers` gate) and `:2182-2192` (stale "no issue status gate" comment)
- Modify: `server/internal/service/builtin_skills/multica-mentioning/SKILL.md` (silent no-op cases) and `server/internal/service/builtin_skills/multica-mentioning/references/mentioning-source-map.md` (CLAUDE.md same-PR rule)
- Modify: `packages/views/issues/blocked-trigger-copy.ts` (two switch cases)
- Modify: `packages/views/locales/en/issues.json`, `packages/views/locales/zh-Hans/issues.json`, `packages/views/locales/ja/issues.json`, `packages/views/locales/ko/issues.json` (two keys each, in the `comment` namespace next to the existing `trigger_blocked_*` keys at en:302-308)
- Test: `server/internal/handler/issue_archive_guard_test.go` (append); `packages/views/issues/components/comment-trigger-chips.test.tsx` (add one case)

**Interfaces:**
- Consumes: `ReasonIssueArchived` from Task 4; `util.ParseMentions` (same parser already used at comment.go:1880); `commentMentionTarget`, `DispatchBlocked` (comment.go / admission.go); fixtures from Tasks 1–4.
- Produces: wire behavior — on an archived issue, `POST /comments/trigger-preview` returns `agents: []` plus one `blocked` outcome per explicit @agent/@squad mention with `reason_code: "issue_archived"`; posting the comment enqueues nothing (mentions AND implicit assignee/thread/conversation routing).

- [ ] **Step 1: Write the failing server tests**

Append to `server/internal/handler/issue_archive_guard_test.go`:

```go
// Fix (c), comment half: no comment raises a run on an archived issue — not
// explicit @mentions, and not the implicit assignee-fallback routing. The
// preview endpoint must warn (blocked outcome) instead of silently no-oping.
func TestCommentTriggerPreviewBlockedOnArchivedIssue(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := createHandlerTestAgent(t, "ArchiveMentionPreview", []byte("[]"))
	issueID := insertAgentAssignedIssue(t, agentID, 92163, "mention-archived-preview")
	updateChildStatus(t, issueID, "archive")

	content := "[@ArchiveMentionPreview](mention://agent/" + agentID + ") please continue"
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues/"+issueID+"/comments/trigger-preview", map[string]any{
		"content": content,
	})
	req = withURLParam(req, "id", issueID)
	testHandler.PreviewCommentTriggers(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("preview: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp CommentTriggerPreviewResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode preview: %v", err)
	}
	if len(resp.Agents) != 0 {
		t.Fatalf("archived issue must trigger no agents, got %d", len(resp.Agents))
	}
	if len(resp.Blocked) != 1 || resp.Blocked[0].ReasonCode != ReasonIssueArchived {
		t.Fatalf("expected one blocked outcome with issue_archived, got %+v", resp.Blocked)
	}
}

func TestCommentOnArchivedIssueDoesNotEnqueue(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := createHandlerTestAgent(t, "ArchiveMentionCreate", []byte("[]"))
	issueID := insertAgentAssignedIssue(t, agentID, 92164, "mention-archived-create")
	updateChildStatus(t, issueID, "archive")

	// Explicit @mention.
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues/"+issueID+"/comments", map[string]any{
		"content": "[@ArchiveMentionCreate](mention://agent/" + agentID + ") go on",
	})
	req = withURLParam(req, "id", issueID)
	testHandler.CreateComment(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateComment mention: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	// Plain comment — would normally fire the assignee fallback in any status.
	w = httptest.NewRecorder()
	req = newRequest("POST", "/api/issues/"+issueID+"/comments", map[string]any{
		"content": "just checking in",
	})
	req = withURLParam(req, "id", issueID)
	testHandler.CreateComment(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateComment plain: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	if got := taskCountForIssue(t, issueID); got != 0 {
		t.Fatalf("comments on an archived issue must not enqueue; got %d task row(s)", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/handler/ -run 'TestCommentTriggerPreviewBlockedOnArchivedIssue|TestCommentOnArchivedIssueDoesNotEnqueue' -v
```

Expected: both FAIL (agents returned in preview; task rows created by both comment forms).

- [ ] **Step 3: Implement the gate**

In `server/internal/handler/comment.go`, at the top of `computeCommentAgentTriggers` (:1874), immediately after the `isNoteComment` early return:

```go
	// Archive (fork status #39) is retired work: no comment raises a new agent
	// run on an archived issue — not explicit @mentions and not the implicit
	// routing fallbacks (assignee / thread parent / conversation). done and
	// cancelled stay mentionable (an agent can reopen them — see
	// resolveMentionedAgentCommentTriggers); archive requires an explicit
	// restore first. Explicit mention targets still get a blocked outcome so
	// the composer warns instead of silently no-oping (MUL-4525). The reason
	// code reveals nothing about any target — the caller can already see the
	// issue's status.
	if issue.Status == "archive" {
		var targets []commentMentionTarget
		seen := make(map[string]struct{})
		for _, m := range util.ParseMentions(content) {
			if m.Type != "agent" && m.Type != "squad" {
				continue
			}
			key := m.Type + ":" + m.ID
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			targets = append(targets, commentMentionTarget{
				TargetType: m.Type,
				TargetID:   m.ID,
				Status:     DispatchBlocked,
				ReasonCode: ReasonIssueArchived,
			})
		}
		return nil, targets
	}
```

Then update the stale doc comment on `resolveMentionedAgentCommentTriggers` (:2182-2192). Replace the line:

```
// Note: no issue status gate here — @mention is an explicit action and should
// work even on done/cancelled issues (the agent can reopen the issue if needed).
```

with:

```
// Note: done/cancelled issues are deliberately not gated here — @mention is an
// explicit action and the agent can reopen them. Archived issues never reach
// this function: computeCommentAgentTriggers blocks them up front (fork
// status #39 — archive means retired, restore first).
```

- [ ] **Step 4: Run server tests to verify they pass**

```bash
go test ./internal/handler/ -run 'TestCommentTriggerPreviewBlockedOnArchivedIssue|TestCommentOnArchivedIssueDoesNotEnqueue' -v
go test ./internal/handler/ -run 'TestPreviewCommentTriggers|TestCreateComment' -v
```

Expected: new tests PASS; existing comment-trigger tests unaffected (non-zero test count on the second run — both prefixes are verified real: `TestPreviewCommentTriggers_*`, `TestCreateComment_*`).

- [ ] **Step 5: Update the multica-mentioning built-in skill (same-PR rule)**

The comment-trigger contract is product behavior documented by `server/internal/service/builtin_skills/multica-mentioning/`:

1. `SKILL.md` frontmatter `description` — the "silent no-op cases" list ends with "(a name where a UUID belongs, a bad/unknown UUID, an already-pending task, an archived agent, a private agent you cannot access)". Extend it to "(…, an archived agent, an archived ISSUE — restore it first, a private agent you cannot access)".
2. `SKILL.md` body — the no-op cases section (around :123, "**An archived agent**, or a squad whose leader is archived: skipped") gains a sibling bullet:

```markdown
- **An archived issue** (fork status #39): NO mention on it triggers anything —
  the run is blocked with reason `issue_archived` until the issue is restored
  to an active status. Unlike done/cancelled issues, which stay mentionable.
```

3. `references/mentioning-source-map.md` — add a line mapping the new gate to `server/internal/handler/comment.go` (`computeCommentAgentTriggers`, archive gate).

- [ ] **Step 6: Frontend copy — locale keys**

In `packages/views/locales/en/issues.json`, inside the `comment` namespace next to `trigger_blocked_runtime_offline` (:304) and `trigger_blocked_short_runtime_offline` (:308), add:

```json
    "trigger_blocked_issue_archived": "This issue is archived — restore it before triggering agents",
    "trigger_blocked_short_issue_archived": "Issue archived",
```

`packages/views/locales/zh-Hans/issues.json` (same positions; the file's own archive term is 「归档/已归档」, see :17):

```json
    "trigger_blocked_issue_archived": "议题已归档，请先恢复再触发智能体",
    "trigger_blocked_short_issue_archived": "议题已归档",
```

Before committing, verify the noun for issue (议题) against `apps/docs/content/docs/developers/conventions.zh.mdx` (repo rule: it is the source of truth for the zh glossary); if the glossary uses a different noun, use that one.

`packages/views/locales/ja/issues.json`:

```json
    "trigger_blocked_issue_archived": "このイシューはアーカイブ済みです。復元するとエージェントをトリガーできます",
    "trigger_blocked_short_issue_archived": "アーカイブ済み",
```

`packages/views/locales/ko/issues.json`:

```json
    "trigger_blocked_issue_archived": "이슈가 보관됨 상태입니다. 복원 후 에이전트를 트리거할 수 있습니다",
    "trigger_blocked_short_issue_archived": "보관됨",
```

- [ ] **Step 7: Frontend copy — mapping cases**

In `packages/views/issues/blocked-trigger-copy.ts`, add to `blockedReasonLabel` (before `default`):

```ts
    case "issue_archived":
      return t(($) => $.comment.trigger_blocked_issue_archived);
```

and to `blockedShortReasonLabel` (before `default`):

```ts
    case "issue_archived":
      return t(($) => $.comment.trigger_blocked_short_issue_archived);
```

The `default` branches stay — unknown future codes must keep rendering the generic copy.

- [ ] **Step 8: Frontend test**

In `packages/views/issues/components/comment-trigger-chips.test.tsx`, locate the existing test that renders a blocked outcome with a known reason code (e.g. `invocation_not_allowed`) and add a sibling case: a blocked outcome with `reason_code: "issue_archived"` renders the "Issue archived" short label (and not the generic fallback). Follow the file's existing render/assert pattern exactly — same helpers, same query style.

- [ ] **Step 9: Run frontend verification**

From the worktree root:

```bash
pnpm --filter @multica/views test
pnpm typecheck
```

Expected: PASS, including `locales/parity.test.ts` (which fails if any of the four bundles is missing the new keys). Run the whole views package suite, not just the issues folder — sibling tests assert on shared components.

- [ ] **Step 10: Commit**

```bash
git add server/internal/handler/comment.go server/internal/handler/issue_archive_guard_test.go server/internal/service/builtin_skills/multica-mentioning/ packages/views/issues/blocked-trigger-copy.ts packages/views/issues/components/comment-trigger-chips.test.tsx packages/views/locales/en/issues.json packages/views/locales/zh-Hans/issues.json packages/views/locales/ja/issues.json packages/views/locales/ko/issues.json
git commit -m "fix(comments): block comment-driven agent runs on archived issues"
```

---

### Task 6: Stage/parent terminal checks include archive — single AND batch paths (fix e)

**Files:**
- Modify: `server/internal/handler/issue_child_done.go:337-342` (`isTerminalChildStatus`), `:90` (single-path parent guard in `notifyParentOfChildDone`), and `:188` (batch-path parent guard in the batch helper — it is a DUPLICATED copy, marked "Same parent guards as the single path")
- Test: `server/internal/handler/issue_child_done_test.go` (append), `server/internal/handler/issue_batch_test.go` (append)

**Interfaces:**
- Consumes: `newChildDoneFixture(t, parentStatus)`, `updateChildStatus`, `countSystemCommentsOn` (`issue_child_done_test.go`); `createIssueViaHTTP` (Task 1; same package).
- Produces: `isTerminalChildStatus` returns true for `"archive"`; every call site (stage barrier :373/:389/:418, batch transition guard `issue.go:3279`) inherits the change automatically. BOTH parent-status guards (single + batch) skip archived parents.

- [ ] **Step 1: Write the failing tests**

Append to `server/internal/handler/issue_child_done_test.go`:

```go
// Fix (e), child side: an archived child is retired — it must close its slot
// in the stage barrier and notify the parent exactly like done/cancelled,
// instead of holding the stage open forever.
func TestChildArchiveNotifiesParent(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	fx := newChildDoneFixture(t, "in_progress")
	updateChildStatus(t, fx.child.ID, "archive")
	if got := countSystemCommentsOn(t, fx.parent.ID); got != 1 {
		t.Fatalf("archived child must notify the parent once, got %d", got)
	}
}

// Terminal -> terminal is a no-op: cancelling then archiving must not
// produce a second parent notification.
func TestChildCancelledThenArchivedDoesNotDoubleNotify(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	fx := newChildDoneFixture(t, "in_progress")
	updateChildStatus(t, fx.child.ID, "cancelled")
	updateChildStatus(t, fx.child.ID, "archive")
	if got := countSystemCommentsOn(t, fx.parent.ID); got != 1 {
		t.Fatalf("cancelled->archive is terminal->terminal, expected 1 notification, got %d", got)
	}
}

// Fix (e), parent side (single path): an archived parent is retired — a child
// completing must not post a system comment on it (which would wake its
// assignee and raise new spend on retired work).
func TestArchivedParentNotWokenByChildDone(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	fx := newChildDoneFixture(t, "in_progress")
	updateChildStatus(t, fx.parent.ID, "archive")
	updateChildStatus(t, fx.child.ID, "done")
	if got := countSystemCommentsOn(t, fx.parent.ID); got != 0 {
		t.Fatalf("archived parent must stay inert, got %d system comment(s)", got)
	}
}
```

Append to `server/internal/handler/issue_batch_test.go` (the batch path has its OWN duplicated parent guard — these tests are not redundant with the single-path ones):

```go
// Fix (e), batch child side: batch-archiving all children must close the
// barrier and notify the parent exactly once.
func TestBatchChildArchiveNotifiesParentOnce(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	fx := newChildDoneFixture(t, "in_progress")
	second := createIssueViaHTTP(t, map[string]any{
		"title":           "batch-archive-child-2",
		"status":          "in_progress",
		"parent_issue_id": fx.parent.ID,
	})
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues/batch-update", map[string]any{
		"issue_ids": []string{fx.child.ID, second.ID},
		"updates":   map[string]any{"status": "archive"},
	})
	testHandler.BatchUpdateIssues(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("batch update: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := countSystemCommentsOn(t, fx.parent.ID); got != 1 {
		t.Fatalf("batch-archiving all children must notify parent exactly once, got %d", got)
	}
}

// Fix (e), batch parent side: the batch path's duplicated parent guard
// (issue_child_done.go:188) must also skip archived parents.
func TestBatchChildDoneSkipsArchivedParent(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	fx := newChildDoneFixture(t, "in_progress")
	updateChildStatus(t, fx.parent.ID, "archive")
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues/batch-update", map[string]any{
		"issue_ids": []string{fx.child.ID},
		"updates":   map[string]any{"status": "done"},
	})
	testHandler.BatchUpdateIssues(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("batch update: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := countSystemCommentsOn(t, fx.parent.ID); got != 0 {
		t.Fatalf("archived parent must stay inert on batch child done, got %d system comment(s)", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/handler/ -run 'TestChildArchiveNotifiesParent|TestChildCancelledThenArchivedDoesNotDoubleNotify|TestArchivedParentNotWokenByChildDone|TestBatchChildArchiveNotifiesParentOnce|TestBatchChildDoneSkipsArchivedParent' -v
```

Expected: `TestChildArchiveNotifiesParent` and `TestBatchChildArchiveNotifiesParentOnce` FAIL (0 comments — archive is not terminal today); `TestArchivedParentNotWokenByChildDone` and `TestBatchChildDoneSkipsArchivedParent` FAIL (1 comment — archived parents are woken). The cancelled→archive test may pass already; keep it as a regression lock.

- [ ] **Step 3: Implement**

In `server/internal/handler/issue_child_done.go`, replace `isTerminalChildStatus` (:337-342):

```go
// isTerminalChildStatus reports whether a child issue status counts as
// "finished" for stage-barrier purposes. Cancelled counts as terminal: a
// cancelled sibling will never complete, so it must not hold a stage open.
// Archive (fork status #39) counts for the same reason — retired work never
// completes either.
func isTerminalChildStatus(status string) bool {
	return status == "done" || status == "cancelled" || status == "archive"
}
```

Widen the single-path parent guard in `notifyParentOfChildDone` (:90):

```go
	if parent.Status == "done" || parent.Status == "cancelled" || parent.Status == "archive" {
		return
	}
```

Widen the batch-path duplicated parent guard (:188 — inside the batch helper's parent loop, marked "Same parent guards as the single path"):

```go
		// Same parent guards as the single path (see notifyParentOfChildDone).
		if parent.Status == "done" || parent.Status == "cancelled" || parent.Status == "archive" {
			continue
		}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/handler/ -run 'TestChild|TestBatchChild|TestArchivedParent' -v
```

Expected: the five new tests PASS and every pre-existing child-done/stage test still PASSes — the regex covers both `TestChildDone*` (single path) and `TestBatchChildDone*` (batch path, `issue_batch_test.go:408+`); confirm both groups appear in the output.

- [ ] **Step 5: Commit**

```bash
git add server/internal/handler/issue_child_done.go server/internal/handler/issue_child_done_test.go server/internal/handler/issue_batch_test.go
git commit -m "fix(issues): treat archive as terminal in stage barriers and parent wake"
```

---

### Task 7: GitHub PR-merge webhook must not resurrect archived issues (fix g)

**Files:**
- Modify: `server/internal/handler/github.go:975-978` (auto-done re-eval guard)
- Test: `server/internal/handler/github_test.go` (append)

**Interfaces:**
- Consumes: the existing merged-PR webhook test scaffolding in `github_test.go` — `TestWebhook_MergedPR_PreservesCancelled` (:346) is the exact template for this case.
- Produces: a merged close-intent PR linked to an archived issue leaves the issue in `archive` (no `advanceIssueToDone`).

- [ ] **Step 1: Write the failing test**

In `server/internal/handler/github_test.go`, duplicate `TestWebhook_MergedPR_PreservesCancelled` (:346) as `TestWebhook_MergedPR_PreservesArchive` with exactly two deltas:

1. the seeded issue's status is `"archive"` instead of `"cancelled"`;
2. the final assertion expects the issue status to still be `"archive"` after the merged-PR webhook is delivered.

Keep every other line (webhook payload construction, signature, PR-link seeding, delivery) identical to the template test.

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/handler/ -run 'TestWebhook_MergedPR_PreservesArchive' -v
```

Expected: FAIL — the issue is advanced to `done` (the re-eval loop only skips `done`/`cancelled`).

- [ ] **Step 3: Implement the guard**

In `server/internal/handler/github.go` (:975-978), widen the skip inside the re-eval loop:

```go
			for _, issue := range reevalIssues {
				// done/cancelled are final; archive (fork status #39) is
				// retired — a PR merging later must not resurrect it.
				if issue.Status == "done" || issue.Status == "cancelled" || issue.Status == "archive" {
					continue
				}
```

Also update the doc comment above the loop (:965-973, rule 1 "the issue isn't already terminal (`done` / `cancelled`)") to mention archive.

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/handler/ -run 'TestWebhook_MergedPR' -v
```

Expected: the new test PASSes and all pre-existing `TestWebhook_MergedPR_*` tests still PASS.

- [ ] **Step 5: Commit**

```bash
git add server/internal/handler/github.go server/internal/handler/github_test.go
git commit -m "fix(github): keep archived issues archived when a linked PR merges"
```

---

### Task 8: Consistency audit, design-doc addendum + full verification

**Files:**
- Modify: `docs/superpowers/specs/2026/05/07/issue-archive-status-design.md` (semantics addendum)
- Otherwise verification-only; produces changes only if the audit finds a missed site (then: fix + test in the pattern of Tasks 1–7).

**Interfaces:**
- Consumes: everything above.
- Produces: a verified, pushable branch with docs matching behavior.

- [ ] **Step 1: Design-doc addendum**

The original archive design (`docs/superpowers/specs/2026/05/07/issue-archive-status-design.md`, "Product Semantics") defines archive as "a closed issue status … matching the user-facing terminal behavior of `cancelled`". This branch deliberately EXTENDS that: unlike done/cancelled, archived issues also refuse manual rerun, comment-driven triggers (explicit and implicit), retries, claims, and webhook auto-done. Append a dated addendum section to that design doc:

```markdown
## Addendum (2026-07-16): archive means retired, stricter than done/cancelled

The original semantics above made archive match `cancelled`'s terminal
behavior. The archive-consistency fixes (branch `fix/archive-consistency`)
tighten this: archive now means **retired work on which no new agent spend can
appear**. Concretely, and unlike `done`/`cancelled`:

- Creating into, assigning onto, or promoting into `archive` never enqueues.
- Manual rerun returns 409 `issue_archived`; restore first.
- No comment triggers on an archived issue — explicit @mentions are blocked
  with reason `issue_archived`; implicit routing (assignee / thread /
  conversation) is suppressed. `done`/`cancelled` issues stay mentionable.
- Tasks on an archived issue are never claimed and never retried, closing the
  enqueue/archive concurrency races at the queued→dispatched chokepoint.
- Archived issues do not count as active duplicates.
- Archive is terminal for stage barriers and parent wake (single and batch
  paths), and a merged close-intent PR does not resurrect an archived issue.

Restoring from archive re-enables all of the above but never auto-enqueues by
itself; runs start again via the normal assign/promote/mention actions.
```

- [ ] **Step 2: Audit remaining terminal-status lists**

```bash
cd server
grep -rn "'done', 'cancelled'" pkg/db/queries/ | grep -v archive
grep -rn '"done" || .* == "cancelled"' internal/ --include="*.go" | grep -v archive | grep -v _test
grep -rn '!= "done" && .* != "cancelled"' internal/ --include="*.go" | grep -v archive | grep -v _test
```

Expected: zero hits on the first command; the Go greps must return no line that decides "is this issue finished/retired" without considering archive (call-site judgment: read each hit; task-status and agent-status lists are out of scope). Known-good sites that already include archive and must still: `internal/daemon/gc.go:308-315`, `internal/handler/issue.go:478,544`, `pkg/db/queries/inbox.sql:90`, `pkg/db/queries/issue.sql:172,328`.

- [ ] **Step 3: Built-in skills doc check (repo rule)**

```bash
grep -rin "rerun\|allow.duplicate\|duplicate detection\|archive" internal/service/builtin_skills/ --include="*.md" | grep -vi "webhook\|skill-importing\|archived agent\|archived skill"
```

Task 5 already updated `multica-mentioning`. If this sweep surfaces any OTHER `SKILL.md` / `references/*-source-map.md` documenting issue rerun, duplicate detection, or archive semantics, update it in this branch (CLAUDE.md requires same-PR doc updates).

- [ ] **Step 4: Full Go verification**

```bash
gofmt -l internal/ pkg/ | grep -v generated   # expected: empty
go vet ./...                                  # expected: clean
go test ./internal/handler/ ./internal/service/ ./internal/issueguard/ ./internal/dispatch/
```

Expected: PASS. Confirm the handler/service output is not "Skipping tests: could not connect to database". Known environmental failures (config tests poisoned by `.env`, pg_cron flakes, agent-CLI tests) live in other packages and are out of this command's scope; if one appears here, investigate rather than dismiss.

- [ ] **Step 5: Full TS verification**

```bash
cd .. && pnpm --filter @multica/views test && pnpm typecheck
```

Expected: PASS.

- [ ] **Step 6: Push and PR**

```bash
git push -u origin fix/archive-consistency
gh pr create --repo furtherref/multica --title "fix(issues): make archive consistently mean retired work" --body "..."
```

PR body: one paragraph per fix (a–g) with the defect, the guard added, and the test that locks it; note the new `issue_archived` reason code is additive (clients switch with a `default` branch); note zero schema changes and the claim/retry predicates as the race-closing chokepoint. Each paragraph must be one continuous line (no hard-wrapping — GitHub renders soft breaks as `<br>`). If the pre-push hook fails only on the Google-Fonts fetch, push with `--no-verify`.

## Self-Review Notes

- Spec coverage: (a) Task 1 (create + assign branches, incl. batch entry point); (b) Task 1 (status branch); (c) Tasks 4 + 5 (rerun, explicit mentions, and implicit comment routing — the plan deliberately gates ALL comment routing, not just mentions, since a plain comment firing the assignee is the same retired-work spend); (d) Task 3 (both queries, both tested); (e) Task 6 (child-side barrier + BOTH parent-side guards — the batch path duplicates the guard at issue_child_done.go:188 and needs its own edit and tests); (f) Task 2 (claim + retry DB predicates close the concurrency races at the single queued→dispatched chokepoint — chosen over cross-path transactions/row locks as the lighter mechanism achieving the same invariant); (g) Task 7 (GitHub webhook). Failure-inbox cleanup from the design doc's prerequisite list was verified already-consistent (`ArchiveCompletedInbox` includes archive) — no task needed.
- Insert-time status predicates on `CreateAgentTask` were deliberately NOT added: the claim guard already guarantees "never executes", `CreateAgentTask` is the hottest insert shared by chat/quick-create (issue_id NULL), and a rare inert queued row on an archived issue is cosmetic. Revisit only if such rows confuse the execution log in practice.
- Deliberately out of scope: restoring-from-archive does not auto-enqueue; localizing the rerun 409 toast (ALL dispatch-blocked reasons currently surface the English fallback via the same toast — a pre-existing pattern, tracked as follow-up, not a regression of this branch); disabling the rerun button client-side on archived issues (same follow-up); mobile's missing archive in status filters (`apps/mobile/.../issues-filter.tsx:23` builds `[...BOARD_STATUSES, "cancelled"]` — a pre-existing parity gap from the original archive rollout, to be filed as its own issue per `apps/mobile/CLAUDE.md`); the ROI design doc lives on another branch and is not edited here.
- Verified-real test names used in every `-run` regex: `TestPreviewIssueTrigger_*`, `TestUpdateIssueReassign*`, `TestPreviewCommentTriggers_*`, `TestCreateComment_*`, `TestRerunIssue_*`, `TestChildDone*`, `TestBatchChildDone*` (issue_batch_test.go:408+), `TestWebhook_MergedPR_*` (github_test.go:234+). Every regression step's expected output includes "non-zero test count" precisely because a zero-match regex exits 0.
- Type consistency: `ReasonIssueArchived` (`"issue_archived"`) is defined once in `internal/dispatch`, re-exported in `handler/admission.go`, referenced in Tasks 4–5 and the frontend switch cases; `taskCountForIssue` / `createIssueViaHTTP` / `insertQueuedIssueTask` are defined once (Tasks 1–2) and reused by name in later tasks (same Go package).
