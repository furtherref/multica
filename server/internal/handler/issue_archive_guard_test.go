package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
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
