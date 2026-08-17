package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/issueguard"
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

func waitForTaskStatus(taskID, want string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var status string
		err := testPool.QueryRow(context.Background(),
			`SELECT status FROM agent_task_queue WHERE id = $1`, taskID,
		).Scan(&status)
		if err == nil && status == want {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

func lockIssueRow(t *testing.T, issueID string) pgx.Tx {
	t.Helper()
	tx, err := testPool.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin issue lock transaction: %v", err)
	}
	if _, err := tx.Exec(context.Background(),
		// FOR NO KEY UPDATE blocks the status write while remaining compatible
		// with the KEY SHARE lock taken by a late task's issue_id FK check.
		`SELECT 1 FROM issue WHERE id = $1 FOR NO KEY UPDATE`, issueID,
	); err != nil {
		_ = tx.Rollback(context.Background())
		t.Fatalf("lock issue row: %v", err)
	}
	return tx
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

// The pre-archive cancellation is deliberately retained as an early failure
// gate, but it cannot close the enqueue/start race by itself. This test locks
// the issue write, waits until that first cancellation has completed, inserts
// a late running task while the issue is still active, then lets the archive
// commit. The post-commit sweep must converge that late task to cancelled.
func TestArchiveCancelsTaskStartedAfterPreWriteSweep(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := createHandlerTestAgent(t, "ArchiveLateStartSweep", []byte("[]"))
	issueID := insertAgentAssignedIssue(t, agentID, 92178, "archive-cancels-late-start")
	initialTaskID := insertRunningIssueTask(t, agentID, issueID)

	tx := lockIssueRow(t, issueID)
	rollback := true
	defer func() {
		if rollback {
			_ = tx.Rollback(context.Background())
		}
	}()

	w := httptest.NewRecorder()
	req := newRequest("PUT", "/api/issues/"+issueID, map[string]any{"status": "archive"})
	req = withURLParam(req, "id", issueID)
	done := make(chan struct{})
	go func() {
		testHandler.UpdateIssue(w, req)
		close(done)
	}()

	if !waitForTaskStatus(initialTaskID, "cancelled", 2*time.Second) {
		_ = tx.Rollback(context.Background())
		rollback = false
		<-done
		t.Fatal("archive handler did not finish its pre-write cancellation")
	}
	lateTaskID := insertRunningIssueTask(t, agentID, issueID)
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatalf("release issue row lock: %v", err)
	}
	rollback = false
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("archive handler did not finish after issue row lock was released")
	}

	if w.Code != http.StatusOK {
		t.Fatalf("UpdateIssue archive: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := taskStatus(t, lateTaskID); got != "cancelled" {
		t.Fatalf("task started after the pre-write sweep must be cancelled after archive commit, got %q", got)
	}
}

func TestBatchArchiveCancelsTaskStartedAfterPreWriteSweep(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := createHandlerTestAgent(t, "BatchArchiveLateStartSweep", []byte("[]"))
	issueID := insertAgentAssignedIssue(t, agentID, 92179, "batch-archive-cancels-late-start")
	initialTaskID := insertRunningIssueTask(t, agentID, issueID)

	tx := lockIssueRow(t, issueID)
	rollback := true
	defer func() {
		if rollback {
			_ = tx.Rollback(context.Background())
		}
	}()
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues/batch-update", map[string]any{
		"issue_ids": []string{issueID},
		"updates":   map[string]any{"status": "archive"},
	})
	done := make(chan struct{})
	go func() {
		testHandler.BatchUpdateIssues(w, req)
		close(done)
	}()

	if !waitForTaskStatus(initialTaskID, "cancelled", 2*time.Second) {
		_ = tx.Rollback(context.Background())
		rollback = false
		<-done
		t.Fatal("batch archive handler did not finish its pre-write cancellation")
	}
	lateTaskID := insertRunningIssueTask(t, agentID, issueID)
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatalf("release issue row lock: %v", err)
	}
	rollback = false
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("batch archive handler did not finish after issue row lock was released")
	}

	if w.Code != http.StatusOK {
		t.Fatalf("BatchUpdateIssues archive: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := taskStatus(t, lateTaskID); got != "cancelled" {
		t.Fatalf("batch archive must cancel a task started after its pre-write sweep, got %q", got)
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

	// Direct-SQL restore (bypassing the handler's straggler sweep) proves the
	// claim predicate is status-driven, not task-poisoning. Product-level
	// restore goes through the handler and cancels stragglers instead — see
	// TestRestoreFromArchiveCancelsStragglerTasks.
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

// Fix wave 2: the FailTask service path must commit the fail and suppress the
// retry (not error) when the issue was archived while the task ran.
func TestFailTaskSuppressesRetryOnArchivedIssue(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := createHandlerTestAgent(t, "ArchiveFailTaskSuppress", []byte("[]"))
	issueID := insertAgentAssignedIssue(t, agentID, 92170, "failtask-archived-suppress")
	taskID := insertRunningIssueTask(t, agentID, issueID)
	if _, err := testPool.Exec(context.Background(), `UPDATE issue SET status = 'archive' WHERE id = $1`, issueID); err != nil {
		t.Fatalf("archive issue: %v", err)
	}

	// Drive TaskService.FailTask directly (the real fail entrypoint wrapping
	// the fail transaction). "timeout" is in retryableReasons, and
	// insertRunningIssueTask leaves the schema defaults attempt=1 <
	// max_attempts=2 with no autopilot link and an issue link, so
	// retryEligible fires and wantRetry is true — CreateRetryTask's archive
	// predicate (WHERE ... issue.status != 'archive') must then suppress the
	// clone via ErrNoRows without failing the fail commit.
	if _, err := testHandler.TaskService.FailTask(context.Background(), parseUUID(taskID), "boom", "", "", "", "timeout", false, ""); err != nil {
		t.Fatalf("FailTask: %v", err)
	}

	var status string
	if err := testPool.QueryRow(context.Background(),
		`SELECT status FROM agent_task_queue WHERE id = $1`, taskID).Scan(&status); err != nil {
		t.Fatalf("read task status: %v", err)
	}
	if status != "failed" {
		t.Fatalf("fail must still commit on archived issue, got %q", status)
	}
	if got := taskCountForIssue(t, issueID); got != 1 {
		t.Fatalf("retry must be suppressed (1 row expected), got %d", got)
	}
}

// Positive control for TestFailTaskSuppressesRetryOnArchivedIssue: the SAME
// FailTask invocation on a NON-archived issue must actually create the retry
// clone. Without this, the archived-issue test could pass vacuously if
// wantRetry never fired in the first place (e.g. a broken retryEligible
// predicate) — this pins that the retry branch is genuinely exercised.
func TestFailTaskCreatesRetryOnNonArchivedIssue(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := createHandlerTestAgent(t, "FailTaskRetryControl", []byte("[]"))
	issueID := insertAgentAssignedIssue(t, agentID, 92171, "failtask-retry-control")
	taskID := insertRunningIssueTask(t, agentID, issueID)

	// Same call shape as TestFailTaskSuppressesRetryOnArchivedIssue, minus the
	// archive step: "timeout" is retryable and insertRunningIssueTask leaves
	// attempt=1 < max_attempts=2, so retryEligible/wantRetry should fire and
	// CreateRetryTask should clone a second row.
	if _, err := testHandler.TaskService.FailTask(context.Background(), parseUUID(taskID), "boom", "", "", "", "timeout", false, ""); err != nil {
		t.Fatalf("FailTask: %v", err)
	}

	var status string
	if err := testPool.QueryRow(context.Background(),
		`SELECT status FROM agent_task_queue WHERE id = $1`, taskID).Scan(&status); err != nil {
		t.Fatalf("read task status: %v", err)
	}
	if status != "failed" {
		t.Fatalf("fail must commit on a non-archived issue, got %q", status)
	}
	if got := taskCountForIssue(t, issueID); got != 2 {
		t.Fatalf("retry must be created on a non-archived issue (2 rows expected), got %d", got)
	}
}

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

// Fix (d), autopilot variant: an archived autopilot-created issue must not
// suppress the autopilot's next run within the dedup window.
func TestArchivedAutopilotIssueIsNotADuplicateBlocker(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	// Seed an archived issue with origin_type='autopilot' and a matching
	// autopilot_run row (fixture pattern copied from autopilot_list_test.go:
	// insertListTestAutopilot + the INSERT INTO autopilot_run block).
	agentID := createHandlerTestAgent(t, "ArchivedAutopilotDupGuard", []byte("[]"))
	autopilotIDStr := insertListTestAutopilot(t, agentID, "archived-autopilot-dup-guard")
	autopilotID := parseUUID(autopilotIDStr)

	seededTitle := "archive-autopilot-dup-title-guard"
	var issueID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (
			workspace_id, title, status, priority, creator_id, creator_type,
			number, position, origin_type, origin_id, created_at
		)
		VALUES ($1, $2, 'archive', 'medium', $3, 'member', $4, 0, 'autopilot', $5, now() - interval '1 minute')
		RETURNING id
	`, testWorkspaceID, seededTitle, testUserID, 92167, autopilotIDStr).Scan(&issueID); err != nil {
		t.Fatalf("seed archived autopilot issue: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, issueID) })

	if _, err := testPool.Exec(ctx, `
		INSERT INTO autopilot_run (autopilot_id, issue_id, source, status, triggered_at)
		VALUES ($1, $2, 'schedule', 'completed', now() - interval '1 minute')
	`, autopilotIDStr, issueID); err != nil {
		t.Fatalf("seed autopilot_run: %v", err)
	}

	// LockIssueDuplicateKey takes a transaction-scoped advisory lock
	// (pg_advisory_xact_lock); mirror autopilot.go:590 and run the guard
	// inside an explicit transaction rather than directly against the pool.
	tx, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer tx.Rollback(ctx)
	qtx := db.New(testPool).WithTx(tx)

	found, ok, err := issueguard.LockAndFindRecentAutopilotDuplicate(
		context.Background(), qtx,
		parseUUID(testWorkspaceID), autopilotID, pgtype.UUID{}, seededTitle, time.Hour,
	)
	if err != nil {
		t.Fatalf("autopilot duplicate lookup: %v", err)
	}
	if ok {
		t.Fatalf("archived autopilot issue must not count as a duplicate, found %s", uuidToString(found.ID))
	}
}

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

// Fix wave 2: a dispatched-never-started task whose issue was archived (and
// whose cancel raced/failed) must not be re-delivered by stale-claim recovery.
// The batch variant (ReclaimStaleDispatchedTasksForRuntimes) carries the same
// predicate; the singular query is exercised as the representative.
func TestReclaimSkipsTasksOnArchivedIssue(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := createHandlerTestAgent(t, "ArchiveReclaimGuard", []byte("[]"))
	issueID := insertAgentAssignedIssue(t, agentID, 92168, "reclaim-archived-guard")
	// Seed a stale dispatched-never-started task (old dispatched_at, no lease).
	var taskID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO agent_task_queue (agent_id, runtime_id, status, priority, issue_id, dispatched_at)
		VALUES ($1, (SELECT runtime_id FROM agent WHERE id = $1), 'dispatched', 0, $2, now() - interval '1 hour')
		RETURNING id::text
	`, agentID, issueID).Scan(&taskID); err != nil {
		t.Fatalf("insert stale dispatched task: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE id = $1`, taskID) })

	ctx := context.Background()
	if _, err := testPool.Exec(ctx, `UPDATE issue SET status = 'archive' WHERE id = $1`, issueID); err != nil {
		t.Fatalf("archive issue: %v", err)
	}
	var runtimeID string
	if err := testPool.QueryRow(ctx, `SELECT runtime_id::text FROM agent WHERE id = $1`, agentID).Scan(&runtimeID); err != nil {
		t.Fatalf("load runtime id: %v", err)
	}
	q := db.New(testPool)
	if _, err := q.ReclaimStaleDispatchedTaskForRuntime(ctx, db.ReclaimStaleDispatchedTaskForRuntimeParams{
		RuntimeID:         parseUUID(runtimeID),
		ClaimRecoverySecs: 60,
		PrepareLeaseSecs:  60,
	}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("reclaim must skip the archived issue's task, got err=%v", err)
	}
}

// getIssueRow loads the raw db.Issue row for direct service/handler calls.
func getIssueRow(t *testing.T, issueID string) db.Issue {
	t.Helper()
	row, err := db.New(testPool).GetIssue(context.Background(), parseUUID(issueID))
	if err != nil {
		t.Fatalf("load issue row: %v", err)
	}
	return row
}

// Fix wave 2: advanceIssueToDone must be a conditional write — a snapshot
// read racing a concurrent archive must not resurrect the issue to done.
func TestAdvanceIssueToDoneSkipsSettledIssue(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	issue := createIssueViaHTTP(t, map[string]any{"title": "advance-settled-guard", "status": "in_progress"})
	// Simulate the race: the webhook holds an active snapshot, but the user
	// archives before the write lands.
	loaded := getIssueRow(t, issue.ID)
	if _, err := testPool.Exec(context.Background(), `UPDATE issue SET status = 'archive' WHERE id = $1`, issue.ID); err != nil {
		t.Fatalf("archive issue: %v", err)
	}
	testHandler.advanceIssueToDone(context.Background(), loaded, testWorkspaceID)
	var status string
	if err := testPool.QueryRow(context.Background(), `SELECT status FROM issue WHERE id = $1`, issue.ID).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status != "archive" {
		t.Fatalf("stale advance must not overwrite archive, got %q", status)
	}
}

func TestAdvanceIssueToDoneAdvancesActiveIssue(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	issue := createIssueViaHTTP(t, map[string]any{"title": "advance-active-ok", "status": "in_progress"})
	testHandler.advanceIssueToDone(context.Background(), getIssueRow(t, issue.ID), testWorkspaceID)
	var status string
	if err := testPool.QueryRow(context.Background(), `SELECT status FROM issue WHERE id = $1`, issue.ID).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status != "done" {
		t.Fatalf("active issue must advance to done, got %q", status)
	}
}

// Fix wave 2: restoring from archive must cancel straggler tasks that raced
// past the archive-time cancel — they were inert under the claim/reclaim
// guards and must not become runnable again.
func TestRestoreFromArchiveCancelsStragglerTasks(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := createHandlerTestAgent(t, "ArchiveRestoreSweep", []byte("[]"))
	issueID := insertAgentAssignedIssue(t, agentID, 92169, "restore-sweeps-stragglers")
	updateChildStatus(t, issueID, "archive")
	// Straggler: inserted AFTER the archive-time cancel ran (simulates the
	// enqueue/retry race).
	taskID := insertQueuedIssueTask(t, agentID, issueID)

	updateChildStatus(t, issueID, "todo")

	var status string
	if err := testPool.QueryRow(context.Background(),
		`SELECT status FROM agent_task_queue WHERE id = $1`, taskID).Scan(&status); err != nil {
		t.Fatalf("read task status: %v", err)
	}
	if status != "cancelled" {
		t.Fatalf("restore must cancel straggler tasks, got %q", status)
	}
}

// Fix wave 2 re-review: restoring AND reassigning in one request must cancel
// the straggler but keep the newly dispatched assign-source run — the sweep
// runs before WillEnqueueRun, never after (MUL-3375: the write path must not
// drop a run the preview promised).
func TestRestoreWithReassignKeepsNewRun(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := createHandlerTestAgent(t, "ArchiveRestoreReassign", []byte("[]"))
	issue := createIssueViaHTTP(t, map[string]any{"title": "restore-reassign-keeps-run", "status": "todo"})
	updateChildStatus(t, issue.ID, "archive")
	stragglerID := insertQueuedIssueTask(t, agentID, issue.ID)

	w := httptest.NewRecorder()
	req := newRequest("PUT", "/api/issues/"+issue.ID, map[string]any{
		"status":        "todo",
		"assignee_type": "agent",
		"assignee_id":   agentID,
	})
	req = withURLParam(req, "id", issue.ID)
	testHandler.UpdateIssue(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("restore+reassign: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var stragglerStatus string
	if err := testPool.QueryRow(context.Background(),
		`SELECT status FROM agent_task_queue WHERE id = $1`, stragglerID).Scan(&stragglerStatus); err != nil {
		t.Fatalf("read straggler status: %v", err)
	}
	if stragglerStatus != "cancelled" {
		t.Fatalf("straggler must be cancelled on restore, got %q", stragglerStatus)
	}
	var queued int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM agent_task_queue WHERE issue_id = $1 AND status = 'queued'`, issue.ID).Scan(&queued); err != nil {
		t.Fatalf("count queued: %v", err)
	}
	if queued != 1 {
		t.Fatalf("the assign-triggered run must survive the sweep, got %d queued", queued)
	}
}

// Fix wave 3: a dispatched task must not START on an archived issue — the
// /start window between archive commit and cancel effect is closed at the
// StartAgentTask statement.
func TestStartTaskRefusedOnArchivedIssue(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := createHandlerTestAgent(t, "ArchiveStartGuard", []byte("[]"))
	issueID := insertAgentAssignedIssue(t, agentID, 92172, "start-archived-guard")
	var taskID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO agent_task_queue (agent_id, runtime_id, status, priority, issue_id, dispatched_at)
		VALUES ($1, (SELECT runtime_id FROM agent WHERE id = $1), 'dispatched', 0, $2, now())
		RETURNING id::text
	`, agentID, issueID).Scan(&taskID); err != nil {
		t.Fatalf("insert dispatched task: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE id = $1`, taskID) })

	ctx := context.Background()
	if _, err := testPool.Exec(ctx, `UPDATE issue SET status = 'archive' WHERE id = $1`, issueID); err != nil {
		t.Fatalf("archive issue: %v", err)
	}
	if _, err := db.New(testPool).StartAgentTask(ctx, parseUUID(taskID)); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("start must refuse the archived issue's task, got err=%v", err)
	}
	var status string
	if err := testPool.QueryRow(ctx, `SELECT status FROM agent_task_queue WHERE id = $1`, taskID).Scan(&status); err != nil {
		t.Fatalf("read task status: %v", err)
	}
	if status != "dispatched" {
		t.Fatalf("refused start must leave the task untouched, got %q", status)
	}
}

// Fix wave 3: the batch restore path sweeps stragglers too.
func TestBatchRestoreFromArchiveCancelsStragglerTasks(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := createHandlerTestAgent(t, "ArchiveBatchRestoreSweep", []byte("[]"))
	issueID := insertAgentAssignedIssue(t, agentID, 92173, "batch-restore-sweeps")
	updateChildStatus(t, issueID, "archive")
	taskID := insertQueuedIssueTask(t, agentID, issueID)

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues/batch-update", map[string]any{
		"issue_ids": []string{issueID},
		"updates":   map[string]any{"status": "todo"},
	})
	testHandler.BatchUpdateIssues(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("batch restore: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var status string
	if err := testPool.QueryRow(context.Background(),
		`SELECT status FROM agent_task_queue WHERE id = $1`, taskID).Scan(&status); err != nil {
		t.Fatalf("read task status: %v", err)
	}
	if status != "cancelled" {
		t.Fatalf("batch restore must cancel stragglers, got %q", status)
	}
}

// insertFailedRetryableIssueTask inserts a FAILED task linked to issueID with
// a retryable failure_reason ("timeout", per retryableReasons) and the
// schema-default attempt/max_attempts (attempt=1 < max_attempts=2, per the
// same default other archive-guard tests rely on — see
// TestFailTaskSuppressesRetryOnArchivedIssue) — the shape retryEligible /
// MaybeRetryFailedTask require to consider a retry at all.
func insertFailedRetryableIssueTask(t *testing.T, agentID, issueID string) string {
	t.Helper()
	var taskID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO agent_task_queue (agent_id, runtime_id, status, priority, issue_id, failure_reason, completed_at)
		VALUES ($1, (SELECT runtime_id FROM agent WHERE id = $1), 'failed', 0, $2, 'timeout', now())
		RETURNING id::text
	`, agentID, issueID).Scan(&taskID); err != nil {
		t.Fatalf("insert failed retryable task: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE id = $1`, taskID) })
	return taskID
}

// Fix wave 3: MaybeRetryFailedTask is the orphan-sweeper's auto-retry path
// (HandleFailedTasks calls it directly on freshly-failed task rows) and
// shares retryEligible/CreateRetryTask with FailTask's in-transaction retry
// — but until now it was only ever exercised indirectly through FailTask.
// This drives it directly: a retry must be suppressed (nil error, no new
// row), not treated as a failure, when the parent's issue was archived
// while the task ran.
func TestMaybeRetryFailedTaskSuppressedOnArchivedIssue(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := createHandlerTestAgent(t, "ArchiveSweeperRetrySuppress", []byte("[]"))
	issueID := insertAgentAssignedIssue(t, agentID, 92174, "sweeper-retry-archived-suppress")
	taskID := insertFailedRetryableIssueTask(t, agentID, issueID)

	ctx := context.Background()
	if _, err := testPool.Exec(ctx, `UPDATE issue SET status = 'archive' WHERE id = $1`, issueID); err != nil {
		t.Fatalf("archive issue: %v", err)
	}

	parent, err := db.New(testPool).GetAgentTask(ctx, parseUUID(taskID))
	if err != nil {
		t.Fatalf("load parent task: %v", err)
	}
	child, err := testHandler.TaskService.MaybeRetryFailedTask(ctx, parent)
	if err != nil {
		t.Fatalf("MaybeRetryFailedTask must suppress, not error, on an archived issue: %v", err)
	}
	if child != nil {
		t.Fatalf("expected no retry child on an archived issue, got task %s", uuidToString(child.ID))
	}
	if got := taskCountForIssue(t, issueID); got != 1 {
		t.Fatalf("expected only the original task row, got %d", got)
	}
}

// Positive control for TestMaybeRetryFailedTaskSuppressedOnArchivedIssue: the
// SAME call on a non-archived issue must create the retry clone. Without
// this, the archived-issue assertion above could pass vacuously if
// retryEligible (or the seeded task shape) never fired a retry in the first
// place.
func TestMaybeRetryFailedTaskCreatesRetryOnNonArchivedIssue(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := createHandlerTestAgent(t, "SweeperRetryControl", []byte("[]"))
	issueID := insertAgentAssignedIssue(t, agentID, 92175, "sweeper-retry-control")
	taskID := insertFailedRetryableIssueTask(t, agentID, issueID)

	ctx := context.Background()
	parent, err := db.New(testPool).GetAgentTask(ctx, parseUUID(taskID))
	if err != nil {
		t.Fatalf("load parent task: %v", err)
	}
	child, err := testHandler.TaskService.MaybeRetryFailedTask(ctx, parent)
	if err != nil {
		t.Fatalf("MaybeRetryFailedTask: %v", err)
	}
	if child == nil {
		t.Fatalf("expected a retry child on a non-archived issue, got nil")
	}
	if got := taskCountForIssue(t, issueID); got != 2 {
		t.Fatalf("expected the original + retry rows, got %d", got)
	}
}

// Fix wave 4: archiving cancels in-flight tasks BEFORE the archive commits —
// a reported archive means the work is already dead.
func TestArchiveCancelsActiveTasks(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := createHandlerTestAgent(t, "ArchiveCancelsActive", []byte("[]"))
	issueID := insertAgentAssignedIssue(t, agentID, 92176, "archive-cancels-active")
	taskID := insertRunningIssueTask(t, agentID, issueID)
	updateChildStatus(t, issueID, "archive")
	var status string
	if err := testPool.QueryRow(context.Background(),
		`SELECT status FROM agent_task_queue WHERE id = $1`, taskID).Scan(&status); err != nil {
		t.Fatalf("read task status: %v", err)
	}
	if status != "cancelled" {
		t.Fatalf("archive must cancel the running task, got %q", status)
	}
}

func TestBatchArchiveCancelsActiveTasks(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := createHandlerTestAgent(t, "BatchArchiveCancelsActive", []byte("[]"))
	issueID := insertAgentAssignedIssue(t, agentID, 92177, "batch-archive-cancels-active")
	taskID := insertRunningIssueTask(t, agentID, issueID)
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues/batch-update", map[string]any{
		"issue_ids": []string{issueID},
		"updates":   map[string]any{"status": "archive"},
	})
	testHandler.BatchUpdateIssues(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("batch archive: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var status string
	if err := testPool.QueryRow(context.Background(),
		`SELECT status FROM agent_task_queue WHERE id = $1`, taskID).Scan(&status); err != nil {
		t.Fatalf("read task status: %v", err)
	}
	if status != "cancelled" {
		t.Fatalf("batch archive must cancel the running task, got %q", status)
	}
}
