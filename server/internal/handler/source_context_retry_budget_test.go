package handler

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/pricing"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// The manual source-context retry mints a task outside every Enqueue* helper,
// so it needs its own budget gate — and the caller must be told the refusal is
// a reached limit (409 budget_exceeded), not a server fault (500). The budget
// lives on a runtime this test creates, so no other suite row is priced by it.
func TestRetrySourceContextQuickCreateRefusesReachedBudget(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	runtimeID := dbfx.Runtime(t, "sc-retry-budget-rt", testutil.Cols{"visibility": "public"})
	agentID := dbfx.Agent(t, "sc-retry-budget-agent", runtimeID, testutil.Cols{"visibility": "workspace"})

	// A completed run worth exactly the daily limit: the budget is reached, not
	// merely approached, so the refusal is the boundary the enforcement uses.
	spentTaskID := dbfx.Task(t, agentID, testutil.Cols{
		"runtime_id": runtimeID, "status": "completed", "originator_source": "direct_human",
		"completed_at": testutil.Raw("now()"),
	})
	dbfx.InsertNoID(t, "task_usage", testutil.Cols{
		"task_id": spentTaskID, "provider": "xai", "model": "grok-4.5",
		"input_tokens": 0, "output_tokens": 0, "cache_read_tokens": 0, "cache_write_tokens": 0,
		"cost_usd_ticks": pricing.USDToTicks(5), "created_at": testutil.Raw("now()"),
	}, "task_id = $1", spentTaskID)
	testutil.Call(t, testHandler.PutRuntimeCostBudget, budgetRequest(t, testUserID, http.MethodPut, runtimeID,
		map[string]any{"runtime": map[string]any{"daily_usd": 5}, "users": []any{}})).Want(http.StatusOK)
	t.Cleanup(func() {
		dbfx.Exec(t, `DELETE FROM runtime_cost_budget WHERE runtime_id = $1`, runtimeID)
		dbfx.Exec(t, `DELETE FROM inbox_item WHERE workspace_id = $1 AND type = 'runtime_budget_exceeded'`, testWorkspaceID)
	})

	contextID := uuid.NewString()
	payload, err := json.Marshal(service.QuickCreateContext{
		Type:            service.QuickCreateContextType,
		Prompt:          "retry source context against a spent budget",
		RequesterID:     testUserID,
		WorkspaceID:     testWorkspaceID,
		SourceContextID: contextID,
	})
	if err != nil {
		t.Fatalf("marshal retry context: %v", err)
	}
	sourceIssueID := dbfx.Issue(t, "sc retry budget source")
	failedTaskID := dbfx.Task(t, agentID, testutil.Cols{
		"runtime_id": runtimeID, "status": "failed", "context": payload,
		"originator_user_id": testUserID, "accountable_user_id": testUserID,
	})
	dbfx.Insert(t, "issue_source_context", testutil.Cols{
		"id": contextID, "workspace_id": testWorkspaceID, "origin_task_id": failedTaskID,
		"source_issue_id": sourceIssueID, "anchor_comment_id": testutil.Raw("gen_random_uuid()"),
		"captured_by_user_id": testUserID, "snapshot_version": 1,
		"snapshot": testutil.Raw("'{}'::jsonb"), "capture_digest": "digest", "state": "pending",
	})

	member, err := testHandler.Queries.GetMemberByUserAndWorkspace(t.Context(), db.GetMemberByUserAndWorkspaceParams{
		UserID:      util.MustParseUUID(testUserID),
		WorkspaceID: util.MustParseUUID(testWorkspaceID),
	})
	if err != nil {
		t.Fatalf("load retry caller membership: %v", err)
	}
	request := withURLParam(newRequest(http.MethodPost, "/api/tasks/"+failedTaskID+"/retry-source-context", nil), "taskId", failedTaskID)
	request = request.WithContext(middleware.SetMemberContext(request.Context(), testWorkspaceID, member))

	var body struct {
		ReasonCode string `json:"reason_code"`
	}
	testutil.Call(t, testHandler.RetrySourceContextQuickCreate, request).Want(http.StatusConflict).JSON(&body)
	if body.ReasonCode != string(ReasonBudgetExceeded) {
		t.Fatalf("reason_code = %q, want %q", body.ReasonCode, ReasonBudgetExceeded)
	}
	if n := dbfx.Count(t, `SELECT count(*) FROM agent_task_queue WHERE rerun_of_task_id = $1`, failedTaskID); n != 0 {
		t.Fatalf("refused retry created %d successor tasks, want 0", n)
	}
	// The context must stay attachable to the original task so the same retry
	// works once the period resets.
	var originTaskID, state string
	dbfx.QueryRow(t, `SELECT origin_task_id::text, state FROM issue_source_context WHERE id = $1`, contextID).Scan(&originTaskID, &state)
	if originTaskID != failedTaskID || state != "pending" {
		t.Fatalf("source context = origin %s state %s, want %s/pending", originTaskID, state, failedTaskID)
	}
}
