package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/pricing"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/testutil"
)

// "Wake now" is a person asking for a run right away, so a reached runtime
// cost budget is answered on the spot with the 409 budget_exceeded every other
// refused dispatch returns, instead of "will run shortly" over a firing the
// budget gate then drops. The refused request records nothing. The budget
// lives on a runtime this test creates, so no other suite row is priced by it.
func TestTriggerIssueWakeupRefusedWhileRuntimeBudgetReached(t *testing.T) {
	runtimeID := dbfx.Runtime(t, "wake-now-budget-rt", testutil.Cols{"visibility": "public"})
	agentID := dbfx.Agent(t, "wake-now-budget-agent", runtimeID, testutil.Cols{"visibility": "workspace"})
	issue := dbfx.Issue(t, "wake now against a runtime budget")
	dbfx.Cleanup(t, "DELETE FROM issue_wakeup_receipt WHERE wakeup_id IN (SELECT id FROM issue_wakeup WHERE issue_id=$1)", issue)
	dbfx.Cleanup(t, "DELETE FROM issue_wakeup WHERE issue_id=$1", issue)
	dbfx.Cleanup(t, "DELETE FROM activity_log WHERE issue_id=$1", issue)
	dbfx.Cleanup(t, "DELETE FROM agent_task_queue WHERE issue_id=$1", issue)

	spentTaskID := dbfx.Task(t, agentID, testutil.Cols{
		"runtime_id": runtimeID, "status": "completed", "originator_source": "direct_human",
		"completed_at": testutil.Raw("now()"),
	})
	dbfx.InsertNoID(t, "task_usage", testutil.Cols{
		"task_id": spentTaskID, "provider": "xai", "model": "grok-4.5",
		"input_tokens": 0, "output_tokens": 0, "cache_read_tokens": 0, "cache_write_tokens": 0,
		"cost_usd_ticks": pricing.USDToTicks(5), "created_at": testutil.Raw("now()"),
	}, "task_id = $1", spentTaskID)
	setDailyBudget := func(usd int) {
		testutil.Call(t, testHandler.PutRuntimeCostBudget, budgetRequest(t, testUserID, http.MethodPut, runtimeID,
			map[string]any{"runtime": map[string]any{"daily_usd": usd}, "users": []any{}})).Want(http.StatusOK)
	}
	t.Cleanup(func() {
		dbfx.Exec(t, `DELETE FROM runtime_cost_budget WHERE runtime_id = $1`, runtimeID)
		dbfx.Exec(t, `DELETE FROM inbox_item WHERE workspace_id = $1 AND type = 'runtime_budget_exceeded'`, testWorkspaceID)
	})

	svc := service.IssueWakeupService{Tasks: testHandler.TaskService}
	w, err := svc.Create(context.Background(), parseUUID(issue), parseUUID(testUserID), pgtype.UUID{}, service.WakeupInput{AgentID: agentID, Kind: "every", IntervalSeconds: 3600, Instruction: "check staging"})
	if err != nil {
		t.Fatal(err)
	}
	id := uuidToString(w.ID)
	wakeNow := func() *httptest.ResponseRecorder {
		t.Helper()
		rec := httptest.NewRecorder()
		testHandler.TriggerIssueWakeup(rec, withURLParams(newRequest("POST", "/", nil), "id", issue, "wakeupID", id))
		return rec
	}
	runs := func() int {
		return dbfx.Count(t, "SELECT count(*) FROM agent_task_queue WHERE context->>'wakeup_id'=$1", id)
	}

	setDailyBudget(10)
	if rec := wakeNow(); rec.Code != http.StatusNoContent {
		t.Fatalf("under budget: %d %s", rec.Code, rec.Body.String())
	}
	if n := runs(); n != 1 {
		t.Fatalf("under budget: %d runs, want 1", n)
	}
	dbfx.Exec(t, "UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE context->>'wakeup_id'=$1", id)

	setDailyBudget(5)
	rec := wakeNow()
	if rec.Code != http.StatusConflict {
		t.Fatalf("budget reached: %d %s", rec.Code, rec.Body.String())
	}
	var body struct {
		ReasonCode string `json:"reason_code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.ReasonCode != "budget_exceeded" {
		t.Fatalf("budget reached: body %s (%v)", rec.Body.String(), err)
	}
	if n := runs(); n != 1 {
		t.Fatalf("budget reached: %d runs, want no new run", n)
	}
	if n := dbfx.Count(t, "SELECT count(*) FROM issue_wakeup_receipt WHERE wakeup_id=$1 AND event_type='wakeup.manual' AND processed_at IS NULL", id); n != 0 {
		t.Fatalf("the refused request left %d manual inputs pending", n)
	}
}
