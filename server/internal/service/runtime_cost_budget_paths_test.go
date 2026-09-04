package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
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

// seedCreateIssueAutopilot creates an active create_issue autopilot
// (agent-assigned) plus a running autopilot_run, mirroring
// seedRunOnlyAutopilot for the other execution mode.
func seedCreateIssueAutopilot(t *testing.T, pool *pgxpool.Pool, workspaceID, agentID, creatorID string) (autopilotID, runID string) {
	t.Helper()
	ctx := context.Background()
	if err := pool.QueryRow(ctx, `
		INSERT INTO autopilot (workspace_id, title, assignee_type, assignee_id, status, execution_mode, created_by_type, created_by_id)
		VALUES ($1, 'create-issue ap', 'agent', $2, 'active', 'create_issue', 'member', $3) RETURNING id`,
		workspaceID, agentID, creatorID).Scan(&autopilotID); err != nil {
		t.Fatalf("seed autopilot: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM autopilot WHERE id = $1`, autopilotID) })
	if err := pool.QueryRow(ctx, `
		INSERT INTO autopilot_run (autopilot_id, source, status) VALUES ($1, 'manual', 'running') RETURNING id`,
		autopilotID).Scan(&runID); err != nil {
		t.Fatalf("seed autopilot run: %v", err)
	}
	return autopilotID, runID
}

// A create_issue autopilot writes the issue first and enqueues afterwards, so
// a refusal at the enqueue would leave a committed issue nobody will ever work
// and a `failed` run feeding the failure-rate auto-pause monitor. The budget is
// therefore checked before any write, and lands as the same `skipped` run
// run_only records.
func TestAutopilotCreateIssueRefusedWhenBudgetReached(t *testing.T) {
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
	autopilotID, runID := seedCreateIssueAutopilot(t, pool, workspaceID, agentID, creatorID)
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
	if strings.ContainsAny(updated.FailureReason.String, "0123456789") {
		t.Fatalf("skipped run reason %q leaks budget amounts", updated.FailureReason.String)
	}
	if n := fx.Count(t, `SELECT count(*) FROM issue WHERE origin_type = 'autopilot' AND origin_id = $1`, autopilotID); n != 0 {
		t.Fatalf("refused create_issue dispatch committed %d issues, want 0", n)
	}
	if n := fx.Count(t, `SELECT count(*) FROM agent_task_queue WHERE autopilot_run_id = $1`, runID); n != 0 {
		t.Fatalf("refused create_issue dispatch queued %d tasks, want 0", n)
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

// The budget gate hangs off the agent row, so a failed GetAgent means the
// budget was never consulted. Retrying anyway spends against a limit the
// server did not read, which is the one thing the gate exists to prevent —
// both entry points must therefore drop the retry instead. An unreadable
// agent stops the sweeper at the gate: no child, and no error either, because
// this is the same "no retry this time" class as an unreadable budget.
func TestRetrySuppressedWhenTheAgentCannotBeLoaded(t *testing.T) {
	pool := newResolveOriginatorPool(t)
	ctx := context.Background()
	q := db.New(pool)
	workspaceID, ownerID, agentID, issueID := seedAttributionFixture(t, pool)
	fx := testutil.New(pool, workspaceID, ownerID)
	agent, err := q.GetAgent(ctx, util.MustParseUUID(agentID))
	if err != nil {
		t.Fatalf("load agent: %v", err)
	}
	taskID := fx.Task(t, agentID, testutil.Cols{
		"runtime_id":     util.UUIDToString(agent.RuntimeID),
		"issue_id":       issueID,
		"status":         "failed",
		"failure_reason": string(taskfailure.ReasonTimeout),
		"attempt":        1,
		"max_attempts":   3,
	})
	parent, err := q.GetAgentTask(ctx, util.MustParseUUID(taskID))
	if err != nil {
		t.Fatalf("load parent: %v", err)
	}

	svc := &TaskService{Queries: q, TxStarter: pool, Bus: events.New()}
	unreadable, cancel := context.WithCancel(ctx)
	cancel()
	child, err := svc.MaybeRetryFailedTask(unreadable, parent)
	if err != nil {
		t.Fatalf("an unreadable agent must stop at the gate, not fall through to the insert: %v", err)
	}
	if child != nil {
		t.Fatalf("retry child created for an unreadable agent: %s", util.UUIDToString(child.ID))
	}
	if n := fx.Count(t, `SELECT count(*) FROM agent_task_queue WHERE parent_task_id = $1`, taskID); n != 0 {
		t.Fatalf("retry children = %d, want 0", n)
	}
}

// A chat turn can be deferred (sealed pending media, retry backoff), so the
// budget sweep can be the thing that kills it. Failing the queue row alone
// leaves the conversation with a spinner and no reply: a terminal chat failure
// owes the transcript an assistant message, exactly as FailTask writes one.
func TestDeferredChatTaskRetiredByBudgetWritesTheAssistantFailure(t *testing.T) {
	pool := newResolveOriginatorPool(t)
	ctx := context.Background()
	q := db.New(pool)
	workspaceID, ownerID, agentID, _ := seedAttributionFixture(t, pool)
	fx := testutil.New(pool, workspaceID, ownerID)
	cleanupBudgetNotices(t, fx, workspaceID)
	agent, err := q.GetAgent(ctx, util.MustParseUUID(agentID))
	if err != nil {
		t.Fatalf("load agent: %v", err)
	}
	runtimeID := util.UUIDToString(agent.RuntimeID)
	daily := 5.0
	seedBudget(t, ctx, q, workspaceID, runtimeID, nil, &daily, nil, nil)

	sessionID := fx.ChatSession(t, agentID)
	chatTaskID := fx.Task(t, agentID, testutil.Cols{
		"runtime_id": runtimeID, "chat_session_id": sessionID, "status": "deferred",
		"fire_at": time.Now().Add(-time.Minute),
	})
	// Spend the budget with a separate completed task so the deferred row is
	// the only thing the sweep can retire.
	seedChatlessSpend(t, ctx, pool, agentID, 5, time.Now().Add(-time.Minute))

	svc := &TaskService{Queries: q, TxStarter: pool, Bus: events.New()}
	if err := svc.PromoteDueDeferredTasksForRuntime(ctx, agent.RuntimeID); err != nil {
		t.Fatalf("PromoteDueDeferredTasksForRuntime: %v", err)
	}

	var status, failureReason string
	fx.QueryRow(t, `SELECT status, COALESCE(failure_reason, '') FROM agent_task_queue WHERE id = $1`, chatTaskID).
		Scan(&status, &failureReason)
	if status != "failed" || failureReason != string(taskfailure.ReasonBudgetExceeded) {
		t.Fatalf("deferred chat task = %s/%s, want failed/budget_exceeded", status, failureReason)
	}
	var role, messageReason, content string
	fx.QueryRow(t, `
		SELECT role, COALESCE(failure_reason, ''), content
		FROM chat_message WHERE task_id = $1`, chatTaskID).Scan(&role, &messageReason, &content)
	if role != "assistant" || messageReason != string(taskfailure.ReasonBudgetExceeded) {
		t.Fatalf("chat outcome message = %s/%s, want assistant/budget_exceeded", role, messageReason)
	}
	if strings.ContainsAny(content, "0123456789") {
		t.Fatalf("assistant failure content %q leaks budget amounts", content)
	}
}

// A quick-create task is retry-eligible, so it too can be sitting deferred when
// the budget is reached. Its requester is waiting on an inbox outcome that
// nothing else will ever write: failing the row silently leaves the pending
// state unresolved and the original prompt unrecoverable.
func TestDeferredQuickCreateRetiredByBudgetNotifiesTheRequester(t *testing.T) {
	pool := newResolveOriginatorPool(t)
	ctx := context.Background()
	q := db.New(pool)
	workspaceID, ownerID, agentID, _ := seedAttributionFixture(t, pool)
	fx := testutil.New(pool, workspaceID, ownerID)
	cleanupBudgetNotices(t, fx, workspaceID)
	f := testutil.New(pool, workspaceID, ownerID)
	f.Cleanup(t, `DELETE FROM inbox_item WHERE workspace_id = $1 AND type = 'quick_create_failed'`, workspaceID)
	agent, err := q.GetAgent(ctx, util.MustParseUUID(agentID))
	if err != nil {
		t.Fatalf("load agent: %v", err)
	}
	runtimeID := util.UUIDToString(agent.RuntimeID)
	daily := 5.0
	seedBudget(t, ctx, q, workspaceID, runtimeID, nil, &daily, nil, nil)

	quickCreate, err := json.Marshal(map[string]string{
		"type":         QuickCreateContextType,
		"prompt":       "file the flaky login bug",
		"requester_id": ownerID,
		"workspace_id": workspaceID,
	})
	if err != nil {
		t.Fatalf("marshal quick-create context: %v", err)
	}
	quickCreateTaskID := fx.Task(t, agentID, testutil.Cols{
		"runtime_id": runtimeID, "status": "deferred",
		"fire_at": time.Now().Add(-time.Minute), "context": string(quickCreate),
	})
	seedChatlessSpend(t, ctx, pool, agentID, 5, time.Now().Add(-time.Minute))

	svc := &TaskService{Queries: q, TxStarter: pool, Bus: events.New()}
	if err := svc.PromoteDueDeferredTasksForRuntime(ctx, agent.RuntimeID); err != nil {
		t.Fatalf("PromoteDueDeferredTasksForRuntime: %v", err)
	}

	var status string
	fx.QueryRow(t, `SELECT status FROM agent_task_queue WHERE id = $1`, quickCreateTaskID).Scan(&status)
	if status != "failed" {
		t.Fatalf("deferred quick-create task = %q, want failed", status)
	}
	if n := fx.Count(t, `
		SELECT count(*) FROM inbox_item
		WHERE workspace_id = $1 AND recipient_id = $2 AND type = 'quick_create_failed'`,
		workspaceID, ownerID); n != 1 {
		t.Fatalf("quick_create_failed notices = %d, want 1", n)
	}
}

// Every blocked agent on one runtime measures itself against the same budget
// rows and the same spend, so the sweep must read them once per runtime rather
// than once per agent. A shared machine with a reached runtime-total cap is
// exactly where the old shape was worst: N agents, N budget lookups, N
// aggregates over task_usage, on every claim poll.
func TestDeferredBudgetSweepPricesEachRuntimeOnce(t *testing.T) {
	pool := newResolveOriginatorPool(t)
	ctx := context.Background()
	q := db.New(pool)
	workspaceID, ownerID, firstAgentID, issueID := seedAttributionFixture(t, pool)
	fx := testutil.New(pool, workspaceID, ownerID)
	cleanupBudgetNotices(t, fx, workspaceID)
	firstAgent, err := q.GetAgent(ctx, util.MustParseUUID(firstAgentID))
	if err != nil {
		t.Fatalf("load agent: %v", err)
	}
	runtimeID := util.UUIDToString(firstAgent.RuntimeID)
	_, secondAgentID := seedSecondOwnerAgent(t, ctx, pool, workspaceID, runtimeID)

	// A runtime-total budget binds both owners' agents at once.
	daily := 5.0
	seedBudget(t, ctx, q, workspaceID, runtimeID, nil, &daily, nil, nil)
	seedSpend(t, ctx, pool, firstAgentID, issueID, 5, time.Now().Add(-time.Minute))

	due := time.Now().Add(-time.Minute)
	firstTaskID := fx.Task(t, firstAgentID, testutil.Cols{
		"runtime_id": runtimeID, "issue_id": issueID, "status": "deferred", "fire_at": due,
	})
	secondTaskID := fx.Task(t, secondAgentID, testutil.Cols{
		"runtime_id": runtimeID, "issue_id": issueID, "status": "deferred", "fire_at": due,
	})

	counting := &countingDBTX{DBTX: pool}
	svc := &TaskService{Queries: db.New(counting), TxStarter: pool, Bus: events.New()}
	svc.failDueDeferredTasksOverBudget(ctx, []pgtype.UUID{firstAgent.RuntimeID})

	if n := counting.calls("ListRuntimeCostBudgets"); n != 1 {
		t.Errorf("budget lookups = %d, want exactly 1 for the runtime", n)
	}
	if n := counting.calls("ListRuntimeSpendByOwner"); n != 1 {
		t.Errorf("spend queries = %d, want exactly 1 for the runtime", n)
	}
	for _, taskID := range []string{firstTaskID, secondTaskID} {
		var status, failureReason string
		fx.QueryRow(t, `SELECT status, COALESCE(failure_reason, '') FROM agent_task_queue WHERE id = $1`, taskID).
			Scan(&status, &failureReason)
		if status != "failed" || failureReason != string(taskfailure.ReasonBudgetExceeded) {
			t.Errorf("task %s = %s/%s, want failed/budget_exceeded", taskID, status, failureReason)
		}
	}
}

// An agent that was rebound to another runtime leaves rows behind on the old
// one. The gate must judge those rows by the runtime they are ON, not by the
// runtime the agent points at now: pricing the new runtime's budget and then
// failing rows on the old one retires work no budget refused. Rows whose agent
// has moved away are simply not this gate's business — they promote or not on
// the old runtime's own terms.
func TestDueDeferredTasksIgnoreAgentsReboundToAnotherRuntime(t *testing.T) {
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
	oldRuntimeID := util.UUIDToString(agent.RuntimeID)

	// A second, over-budget runtime, and an agent that has been rebound to it
	// while its deferred row stayed on the old one.
	var newRuntimeID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO agent_runtime (workspace_id, name, runtime_mode, provider, status, device_info, metadata, owner_id)
		VALUES ($1, 'rebound-runtime', 'cloud', 'codex', 'online', '', '{}'::jsonb, $2)
		RETURNING id`, workspaceID, ownerID).Scan(&newRuntimeID); err != nil {
		t.Fatalf("seed second runtime: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, newRuntimeID)
	})

	reboundUserID, reboundAgentID := seedSecondOwnerAgent(t, ctx, pool, workspaceID, newRuntimeID)
	daily := 5.0
	seedBudget(t, ctx, q, workspaceID, newRuntimeID, &reboundUserID, &daily, nil, nil)
	seedSpend(t, ctx, pool, reboundAgentID, issueID, 5, time.Now().Add(-time.Minute))

	leftBehindTaskID := fx.Task(t, reboundAgentID, testutil.Cols{
		"runtime_id": oldRuntimeID, "issue_id": issueID, "status": "deferred",
		"fire_at": time.Now().Add(-time.Minute),
	})

	svc := &TaskService{Queries: q, TxStarter: pool, Bus: events.New()}
	if err := svc.PromoteDueDeferredTasksForRuntime(ctx, agent.RuntimeID); err != nil {
		t.Fatalf("PromoteDueDeferredTasksForRuntime: %v", err)
	}

	var status, failureReason string
	fx.QueryRow(t, `SELECT status, COALESCE(failure_reason, '') FROM agent_task_queue WHERE id = $1`, leftBehindTaskID).
		Scan(&status, &failureReason)
	if failureReason == string(taskfailure.ReasonBudgetExceeded) {
		t.Fatalf("a rebound agent's row on the old runtime was retired by the NEW runtime's budget (status %s)", status)
	}
	if status != "queued" {
		t.Fatalf("left-behind deferred task = %q, want queued: the old runtime has no budget", status)
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
