package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// A wakeup starts its run outside every Enqueue* helper, so it carries the
// fork's runtime cost budget gate itself. Under the budget the rule starts its
// run. Once the budget is reached a firing starts none: its input is used up,
// the rule stays on and says why, and the owner gets the budget notice. When
// the budget allows runs again, the next firing starts one and clears the
// message.
func TestIssueWakeupRunRefusedWhileRuntimeBudgetReached(t *testing.T) {
	f, s, issue, agentID := wakeFixture(t)
	ctx := context.Background()
	cleanupBudgetNotices(t, f.Fixture, f.WorkspaceID)
	agent, err := f.q.GetAgent(ctx, parseTestUUID(t, agentID))
	if err != nil {
		t.Fatal(err)
	}
	runtimeID := util.UUIDToString(agent.RuntimeID)
	setDailyBudget := func(usd float64) {
		seedBudget(t, ctx, f.q, f.WorkspaceID, runtimeID, nil, &usd, nil, nil)
	}
	seedSpend(t, ctx, f.Pool, agentID, util.UUIDToString(issue), 5, time.Now().Add(-time.Minute))
	w := wakeCreate(t, f, s, issue, WakeupInput{AgentID: agentID, Kind: "event", Mode: "continuous", EventTypes: []string{"comment.created"}, Instruction: "check"})
	completeRuns := func() {
		f.Exec(t, "UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE context->>'wakeup_id'=$1", util.UUIDToString(w.ID))
	}
	rule := func() db.IssueWakeup {
		got, err := f.q.GetIssueWakeup(ctx, db.GetIssueWakeupParams{ID: w.ID, WorkspaceID: w.WorkspaceID})
		if err != nil {
			t.Fatal(err)
		}
		return got
	}

	setDailyBudget(10)
	f.Comment(t, util.UUIDToString(issue), "under budget")
	wakeDispatch(t, s, w)
	if n := wakeRuns(t, f, w.ID); n != 1 {
		t.Fatalf("under budget: %d runs, want 1", n)
	}
	completeRuns()

	setDailyBudget(5)
	f.Comment(t, util.UUIDToString(issue), "budget reached")
	wakeDispatch(t, s, w)
	if n := wakeRuns(t, f, w.ID); n != 1 {
		t.Fatalf("budget reached: %d runs, want no new run", n)
	}
	if n := f.Count(t, "SELECT count(*) FROM issue_wakeup_receipt WHERE wakeup_id=$1 AND processed_at IS NULL", w.ID); n != 0 {
		t.Fatalf("the refused firing left %d inputs pending", n)
	}
	if got := rule(); !got.Enabled || !strings.Contains(got.LastError.String, "cost budget") {
		t.Fatalf("after the refusal: enabled=%t last_error=%q", got.Enabled, got.LastError.String)
	}
	if n := f.Count(t, "SELECT count(*) FROM inbox_item WHERE workspace_id=$1 AND type='runtime_budget_exceeded'", f.WorkspaceID); n == 0 {
		t.Fatal("the refusal filed no budget notice")
	}

	setDailyBudget(100)
	f.Comment(t, util.UUIDToString(issue), "budget raised")
	wakeDispatch(t, s, w)
	if n := wakeRuns(t, f, w.ID); n != 2 {
		t.Fatalf("budget raised: %d runs, want 2", n)
	}
	if got := rule(); got.LastError.Valid {
		t.Fatalf("the next run left the refusal message: %q", got.LastError.String)
	}
}
