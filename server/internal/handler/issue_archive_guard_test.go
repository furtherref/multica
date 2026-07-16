package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
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
