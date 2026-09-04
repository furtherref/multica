package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/dispatch"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/taskfailure"
)

// cleanupBudgetNotices removes the inbox rows checkRuntimeCostBudget files, so
// a refusal in one test cannot be counted by another.
func cleanupBudgetNotices(t *testing.T, f *testutil.Fixture, workspaceID string) {
	t.Helper()
	f.Cleanup(t, `DELETE FROM inbox_item WHERE workspace_id = $1 AND type = 'runtime_budget_exceeded'`, workspaceID)
}

// A run_only autopilot resolves its executing agent itself and writes the task
// row directly, so it needs its own gate. A reached budget must land as a
// `skipped` run carrying budget_exceeded — the same shape the readiness and
// attribution gates use — never as a failed run or an enqueued task.
func TestAutopilotRunOnlyRefusedWhenBudgetReached(t *testing.T) {
	pool := newResolveOriginatorPool(t)
	ctx := context.Background()
	q := db.New(pool)
	workspaceID, creatorID, agentID, issueID := seedAttributionFixture(t, pool)
	fx := testutil.New(pool, workspaceID, creatorID)
	cleanupBudgetNotices(t, fx, workspaceID)
	agent, err := q.GetAgent(ctx, util.MustParseUUID(agentID))
	if err != nil {
		t.Fatalf("load agent: %v", err)
	}
	autopilotID, runID := seedRunOnlyAutopilot(t, pool, workspaceID, agentID, creatorID)
	daily := 5.0
	seedBudget(t, ctx, q, workspaceID, util.UUIDToString(agent.RuntimeID), nil, &daily, nil, nil)
	seedSpend(t, ctx, pool, agentID, issueID, 5, time.Now().Add(-time.Minute))

	svc := &AutopilotService{
		Queries: q, TxStarter: pool, Bus: events.New(),
		TaskSvc: &TaskService{Queries: q, TxStarter: pool, Bus: events.New()},
	}
	ap, err := q.GetAutopilot(ctx, util.MustParseUUID(autopilotID))
	if err != nil {
		t.Fatalf("get autopilot: %v", err)
	}
	run, err := q.GetAutopilotRun(ctx, util.MustParseUUID(runID))
	if err != nil {
		t.Fatalf("get run: %v", err)
	}

	updated, code, err := svc.dispatchAutopilotRun(ctx, ap, pgtype.UUID{}, "manual", &run, util.MustParseUUID(creatorID))
	if err != nil {
		t.Fatalf("a budget refusal must not be a dispatch error: %v", err)
	}
	if code != dispatch.ReasonBudgetExceeded {
		t.Fatalf("reason code = %q, want %q", code, dispatch.ReasonBudgetExceeded)
	}
	if updated.Status != "skipped" {
		t.Fatalf("run status = %q, want skipped", updated.Status)
	}
	if !updated.ReasonCode.Valid || updated.ReasonCode.String != string(dispatch.ReasonBudgetExceeded) {
		t.Fatalf("stored reason_code = %+v, want budget_exceeded", updated.ReasonCode)
	}
	if n := fx.Count(t, `SELECT count(*) FROM agent_task_queue WHERE autopilot_run_id = $1`, runID); n != 0 {
		t.Fatalf("refused run_only dispatch queued %d tasks, want 0", n)
	}
	// autopilot_run.failure_reason is readable by anyone who can see the
	// autopilot, so the refusal string must not carry the limit or the running
	// total that GET /budget gates behind runtime read access.
	if strings.ContainsAny(updated.FailureReason.String, "0123456789") {
		t.Fatalf("skipped run reason %q leaks budget amounts", updated.FailureReason.String)
	}
}

// The coordinator wake-up after a delegated worker failure creates a task of
// its own. A reached budget must stop it without failing the sweep: no recovery
// task, no error, and the recovery comment stays so a later period can replay.
func TestDelegatedFailureRecoveryRefusedWhenBudgetReached(t *testing.T) {
	f, svc := seedDelegatedFailureFixture(t)
	ctx := context.Background()
	q := db.New(f.pool)
	fx := testutil.New(f.pool, f.workspaceID, f.userID)
	cleanupBudgetNotices(t, fx, f.workspaceID)
	daily := 5.0
	seedBudget(t, ctx, q, f.workspaceID, f.runtimeID, nil, &daily, nil, nil)
	seedSpend(t, ctx, f.pool, f.coordinator, f.issueID, 5, time.Now().Add(-time.Minute))

	failedID := f.insertWorkerTask(t, "running", "comment", 1, 2)
	if _, err := svc.FailTask(ctx, failedID, "worker crashed", "", "", "", "agent_error.process_failure", false, "", ""); err != nil {
		t.Fatalf("FailTask: %v", err)
	}

	if n := fx.Count(t, `
		SELECT count(*) FROM agent_task_queue
		WHERE trigger_evidence_kind = 'delegated_failure' AND trigger_evidence_ref_id = $1`, failedID); n != 0 {
		t.Fatalf("budget-refused recovery created %d coordinator tasks, want 0", n)
	}
	// The obligation survives the refusal: the comment is the durable outbox
	// entry a later sweep replays once the period resets.
	if n := fx.Count(t, `
		SELECT count(*) FROM comment
		WHERE issue_id = $1 AND type = 'progress_update' AND source_task_id = $2`, f.issueID, failedID); n != 1 {
		t.Fatalf("recovery comments = %d, want 1", n)
	}

	result, err := svc.RecoverPendingDelegatedFailures(ctx, 20)
	if err != nil {
		t.Fatalf("a budget refusal must not error the sweep: %v", err)
	}
	if result.Blocked == 0 {
		t.Fatalf("sweep result = %+v, want at least one budget-blocked entry", result)
	}
	if result.Replayed != 0 {
		t.Fatalf("sweep replayed %d entries, want 0", result.Replayed)
	}
}

// A reached budget stops automatic retries too: the parent keeps its own
// failure reason and no child is created, on both retry entry points.
func TestAutoRetrySuppressedWhenBudgetReached(t *testing.T) {
	f, svc := seedDelegatedFailureFixture(t)
	ctx := context.Background()
	q := db.New(f.pool)
	fx := testutil.New(f.pool, f.workspaceID, f.userID)
	cleanupBudgetNotices(t, fx, f.workspaceID)
	daily := 5.0
	seedBudget(t, ctx, q, f.workspaceID, f.runtimeID, nil, &daily, nil, nil)
	seedSpend(t, ctx, f.pool, f.coordinator, f.issueID, 5, time.Now().Add(-time.Minute))

	// FailTask's in-transaction retry: 'timeout' is retryable and the task has
	// attempts left, so only the budget can stop the child.
	failedID := f.insertWorkerTask(t, "running", "", 1, 3)
	failed, err := svc.FailTask(ctx, failedID, "server-side timeout", "", "", "", string(taskfailure.ReasonTimeout), false, "", "")
	if err != nil {
		t.Fatalf("FailTask: %v", err)
	}
	if failed.Status != "failed" || failed.FailureReason.String != string(taskfailure.ReasonTimeout) {
		t.Fatalf("parent = %s/%s, want failed/timeout", failed.Status, failed.FailureReason.String)
	}
	if n := fx.Count(t, `SELECT count(*) FROM agent_task_queue WHERE parent_task_id = $1`, failedID); n != 0 {
		t.Fatalf("FailTask created %d retry children over a reached budget, want 0", n)
	}

	// MaybeRetryFailedTask, the sweeper entry point, must reach the same answer
	// for the same row.
	child, err := svc.MaybeRetryFailedTask(ctx, *failed)
	if err != nil {
		t.Fatalf("MaybeRetryFailedTask: %v", err)
	}
	if child != nil {
		t.Fatalf("sweeper retried over a reached budget: %s", util.UUIDToString(child.ID))
	}
	if n := fx.Count(t, `SELECT count(*) FROM agent_task_queue WHERE parent_task_id = $1`, failedID); n != 0 {
		t.Fatalf("sweeper created %d retry children over a reached budget, want 0", n)
	}
}

// Without budget rows the same failure still produces a retry child — the gate
// above must be the budget, not the fixture.
func TestAutoRetryStillRunsWithoutBudgetRows(t *testing.T) {
	f, svc := seedDelegatedFailureFixture(t)
	ctx := context.Background()
	fx := testutil.New(f.pool, f.workspaceID, f.userID)

	failedID := f.insertWorkerTask(t, "running", "", 1, 3)
	if _, err := svc.FailTask(ctx, failedID, "server-side timeout", "", "", "", string(taskfailure.ReasonTimeout), false, "", ""); err != nil {
		t.Fatalf("FailTask: %v", err)
	}
	if n := fx.Count(t, `SELECT count(*) FROM agent_task_queue WHERE parent_task_id = $1`, failedID); n != 1 {
		t.Fatalf("retry children = %d, want 1", n)
	}
}

// A deferred fallback is checked when it comes due, not when it was armed. A
// blocked agent's due row is failed with budget_exceeded instead of becoming
// claimable, and every other agent's row in the same sweep still promotes.
func TestDueDeferredTaskFailedWhenBudgetReached(t *testing.T) {
	pool := newResolveOriginatorPool(t)
	ctx := context.Background()
	q := db.New(pool)
	workspaceID, ownerID, agentID, issueID := seedAttributionFixture(t, pool)
	fx := testutil.New(pool, workspaceID, ownerID)
	cleanupBudgetNotices(t, fx, workspaceID)
	agent, err := q.GetAgent(ctx, util.MustParseUUID(agentID))
	if err != nil {
		t.Fatalf("load agent: %v", err)
	}
	runtimeID := util.UUIDToString(agent.RuntimeID)

	// The budget is scoped to the second owner, so only their agent is blocked
	// and the fixture agent proves the sweep still promotes everyone else.
	blockedUserID, blockedAgentID := seedSecondOwnerAgent(t, ctx, pool, workspaceID, runtimeID)
	daily := 5.0
	seedBudget(t, ctx, q, workspaceID, runtimeID, &blockedUserID, &daily, nil, nil)
	seedSpend(t, ctx, pool, blockedAgentID, issueID, 5, time.Now().Add(-time.Minute))

	due := time.Now().Add(-time.Minute)
	okTaskID := fx.Task(t, agentID, testutil.Cols{
		"runtime_id": runtimeID, "issue_id": issueID, "status": "deferred", "fire_at": due,
	})
	blockedTaskID := fx.Task(t, blockedAgentID, testutil.Cols{
		"runtime_id": runtimeID, "issue_id": issueID, "status": "deferred", "fire_at": due,
	})

	svc := &TaskService{Queries: q, TxStarter: pool, Bus: events.New()}
	if err := svc.PromoteDueDeferredTasksForRuntime(ctx, agent.RuntimeID); err != nil {
		t.Fatalf("PromoteDueDeferredTasksForRuntime: %v", err)
	}

	var status, failureReason, taskError string
	fx.QueryRow(t, `SELECT status, COALESCE(failure_reason, ''), COALESCE(error, '') FROM agent_task_queue WHERE id = $1`, blockedTaskID).
		Scan(&status, &failureReason, &taskError)
	if status != "failed" || failureReason != string(taskfailure.ReasonBudgetExceeded) {
		t.Fatalf("blocked deferred task = %s/%s, want failed/budget_exceeded", status, failureReason)
	}
	// agent_task_queue.error is broadcast on task:failed to every workspace
	// subscriber, so it must name the cause without the amounts GET /budget
	// gates behind runtime read access.
	if strings.ContainsAny(taskError, "0123456789") {
		t.Fatalf("persisted task error %q leaks budget amounts", taskError)
	}
	fx.QueryRow(t, `SELECT status FROM agent_task_queue WHERE id = $1`, okTaskID).Scan(&status)
	if status != "queued" {
		t.Fatalf("unblocked deferred task = %q, want queued (one refusal must not stop the sweep)", status)
	}
	if n := fx.Count(t, `SELECT count(*) FROM inbox_item WHERE workspace_id = $1 AND type = 'runtime_budget_exceeded'`, workspaceID); n == 0 {
		t.Fatal("a refused promotion must file the budget notice")
	}
}

// The retry helper both auto-retry entry points share fails closed: an
// unreadable budget suppresses the retry rather than spending against a limit
// the server could not check.
func TestRetrySuppressedWhenBudgetCheckFails(t *testing.T) {
	pool := newResolveOriginatorPool(t)
	ctx := context.Background()
	q := db.New(pool)
	_, _, agentID, _ := seedAttributionFixture(t, pool)
	agent, err := q.GetAgent(ctx, util.MustParseUUID(agentID))
	if err != nil {
		t.Fatalf("load agent: %v", err)
	}
	svc := &TaskService{Queries: q, TxStarter: pool, Bus: events.New()}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if !svc.retrySuppressedByRuntimeBudget(cancelled, db.AgentTaskQueue{AgentID: agent.ID}, agent) {
		t.Fatal("an unreadable budget must suppress the retry")
	}
	if svc.retrySuppressedByRuntimeBudget(ctx, db.AgentTaskQueue{AgentID: agent.ID}, agent) {
		t.Fatal("no budget rows must not suppress the retry")
	}
}
