package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/pricing"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// seedSpend records one completed task on the fixture agent with a
// provider-priced usage row worth usd dollars, created at createdAt.
func seedSpend(t *testing.T, ctx context.Context, pool *pgxpool.Pool, agentID, issueID string, usd float64, createdAt time.Time) {
	t.Helper()
	var taskID string
	err := pool.QueryRow(ctx, `
		INSERT INTO agent_task_queue (agent_id, runtime_id, issue_id, status, priority, originator_source, completed_at)
		VALUES ($1, (SELECT runtime_id FROM agent WHERE id = $1), $2, 'completed', 0, 'delegation', $3)
		RETURNING id`, agentID, issueID, createdAt).Scan(&taskID)
	if err != nil {
		t.Fatalf("seed task: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM agent_task_queue WHERE id = $1`, taskID) })
	if _, err := pool.Exec(ctx, `
		INSERT INTO task_usage (task_id, provider, model, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, cost_usd_ticks, created_at)
		VALUES ($1, 'xai', 'grok-4.5', 0, 0, 0, 0, $2, $3)`, taskID, pricing.USDToTicks(usd), createdAt); err != nil {
		t.Fatalf("seed usage: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM task_usage WHERE task_id = $1`, taskID) })
}

func seedBudget(t *testing.T, ctx context.Context, q *db.Queries, workspaceID, runtimeID string, userID *string, daily, weekly, monthly *float64) db.RuntimeCostBudget {
	t.Helper()
	toTicks := func(v *float64) pgtype.Int8 {
		if v == nil {
			return pgtype.Int8{}
		}
		return pgtype.Int8{Int64: pricing.USDToTicks(*v), Valid: true}
	}
	params := db.UpsertRuntimeCostBudgetParams{
		ID:                   util.MustParseUUID(newAutopilotIdempotencyKey()),
		WorkspaceID:          util.MustParseUUID(workspaceID),
		RuntimeID:            util.MustParseUUID(runtimeID),
		DailyLimitUsdTicks:   toTicks(daily),
		WeeklyLimitUsdTicks:  toTicks(weekly),
		MonthlyLimitUsdTicks: toTicks(monthly),
	}
	if userID != nil {
		params.UserID = util.MustParseUUID(*userID)
	}
	row, err := q.UpsertRuntimeCostBudget(ctx, params)
	if err != nil {
		t.Fatalf("seed budget: %v", err)
	}
	t.Cleanup(func() { _ = q.DeleteRuntimeCostBudgetsForRuntime(ctx, row.RuntimeID) })
	return row
}

func TestCheckRuntimeCostBudgetPassesWithoutRows(t *testing.T) {
	pool := newResolveOriginatorPool(t)
	ctx := context.Background()
	q := db.New(pool)
	_, _, agentID, _ := seedAttributionFixture(t, pool)
	agent, err := q.GetAgent(ctx, util.MustParseUUID(agentID))
	if err != nil {
		t.Fatal(err)
	}
	svc := &TaskService{Queries: q, TxStarter: pool, Bus: events.New()}
	if err := svc.checkRuntimeCostBudget(ctx, q, agent, time.Now()); err != nil {
		t.Fatalf("expected nil without budget rows, got %v", err)
	}
}

func TestCheckRuntimeCostBudgetRefusesWhenRuntimeTotalReached(t *testing.T) {
	pool := newResolveOriginatorPool(t)
	ctx := context.Background()
	q := db.New(pool)
	workspaceID, _, agentID, issueID := seedAttributionFixture(t, pool)
	agent, _ := q.GetAgent(ctx, util.MustParseUUID(agentID))
	runtimeID := util.UUIDToString(agent.RuntimeID)
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	daily := 20.0
	seedBudget(t, ctx, q, workspaceID, runtimeID, nil, &daily, nil, nil)
	seedSpend(t, ctx, pool, agentID, issueID, 20.31, now.Add(-time.Hour))

	svc := &TaskService{Queries: q, TxStarter: pool, Bus: events.New()}
	err := svc.checkRuntimeCostBudget(ctx, q, agent, now)
	var exceeded *RuntimeBudgetExceededError
	if !errors.As(err, &exceeded) {
		t.Fatalf("expected RuntimeBudgetExceededError, got %v", err)
	}
	if exceeded.Scope != RuntimeBudgetScopeRuntime || exceeded.Period != pricing.PeriodDaily {
		t.Fatalf("scope/period = %s/%s", exceeded.Scope, exceeded.Period)
	}
	if exceeded.UsedTicks != pricing.USDToTicks(20.31) || exceeded.LimitTicks != pricing.USDToTicks(20) {
		t.Fatalf("used/limit = %d/%d", exceeded.UsedTicks, exceeded.LimitTicks)
	}
	if !exceeded.ResetAt.Equal(time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("reset_at = %s", exceeded.ResetAt)
	}
}

func TestCheckRuntimeCostBudgetIgnoresSpendBeforePeriodStart(t *testing.T) {
	pool := newResolveOriginatorPool(t)
	ctx := context.Background()
	q := db.New(pool)
	workspaceID, _, agentID, issueID := seedAttributionFixture(t, pool)
	agent, _ := q.GetAgent(ctx, util.MustParseUUID(agentID))
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	daily := 20.0
	seedBudget(t, ctx, q, workspaceID, util.UUIDToString(agent.RuntimeID), nil, &daily, nil, nil)
	// Yesterday's spend belongs to yesterday's UTC day.
	seedSpend(t, ctx, pool, agentID, issueID, 50, now.Add(-13*time.Hour))
	svc := &TaskService{Queries: q, TxStarter: pool, Bus: events.New()}
	if err := svc.checkRuntimeCostBudget(ctx, q, agent, now); err != nil {
		t.Fatalf("expected pass, got %v", err)
	}
}

func TestCheckRuntimeCostBudgetPerUserBlocksOnlyThatOwner(t *testing.T) {
	pool := newResolveOriginatorPool(t)
	ctx := context.Background()
	q := db.New(pool)
	workspaceID, ownerID, agentID, issueID := seedAttributionFixture(t, pool)
	agent, _ := q.GetAgent(ctx, util.MustParseUUID(agentID))
	runtimeID := util.UUIDToString(agent.RuntimeID)
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	weekly := 10.0
	seedBudget(t, ctx, q, workspaceID, runtimeID, &ownerID, nil, &weekly, nil)
	seedSpend(t, ctx, pool, agentID, issueID, 12, now.Add(-24*time.Hour))

	svc := &TaskService{Queries: q, TxStarter: pool, Bus: events.New()}
	var exceeded *RuntimeBudgetExceededError
	if err := svc.checkRuntimeCostBudget(ctx, q, agent, now); !errors.As(err, &exceeded) || exceeded.Scope != RuntimeBudgetScopeUser {
		t.Fatalf("expected user-scope refusal, got %v", err)
	}

	// A second agent on the same runtime owned by nobody is not capped by the
	// owner's row.
	var otherAgentID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO agent (workspace_id, name, runtime_mode, runtime_config, runtime_id, visibility,
			max_concurrent_tasks, instructions, custom_env, custom_args)
		VALUES ($1, 'budget-other', 'cloud', '{}'::jsonb, $2, 'workspace', 1, '', '{}'::jsonb, '[]'::jsonb)
		RETURNING id`, workspaceID, runtimeID).Scan(&otherAgentID); err != nil {
		t.Fatalf("seed other agent: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM agent WHERE id = $1`, otherAgentID) })
	other, _ := q.GetAgent(ctx, util.MustParseUUID(otherAgentID))
	if err := svc.checkRuntimeCostBudget(ctx, q, other, now); err != nil {
		t.Fatalf("other owner must pass, got %v", err)
	}
}

func TestCheckRuntimeCostBudgetUnpricedTokensCountAsZero(t *testing.T) {
	pool := newResolveOriginatorPool(t)
	ctx := context.Background()
	q := db.New(pool)
	workspaceID, _, agentID, issueID := seedAttributionFixture(t, pool)
	agent, _ := q.GetAgent(ctx, util.MustParseUUID(agentID))
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	daily := 1.0
	seedBudget(t, ctx, q, workspaceID, util.UUIDToString(agent.RuntimeID), nil, &daily, nil, nil)
	var taskID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO agent_task_queue (agent_id, runtime_id, issue_id, status, priority, originator_source, completed_at)
		VALUES ($1, (SELECT runtime_id FROM agent WHERE id = $1), $2, 'completed', 0, 'delegation', $3)
		RETURNING id`, agentID, issueID, now.Add(-time.Hour)).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM agent_task_queue WHERE id = $1`, taskID) })
	if _, err := pool.Exec(ctx, `
		INSERT INTO task_usage (task_id, provider, model, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, created_at)
		VALUES ($1, 'copilot', 'model-nobody-prices', 50000000, 50000000, 0, 0, $2)`, taskID, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM task_usage WHERE task_id = $1`, taskID) })
	svc := &TaskService{Queries: q, TxStarter: pool, Bus: events.New()}
	if err := svc.checkRuntimeCostBudget(ctx, q, agent, now); err != nil {
		t.Fatalf("unpriced usage must not count, got %v", err)
	}
}

func TestEnqueueTaskForIssueRefusedWhenBudgetReached(t *testing.T) {
	pool := newResolveOriginatorPool(t)
	ctx := context.Background()
	q := db.New(pool)
	workspaceID, creatorID, agentID, issueID := seedAttributionFixture(t, pool)
	agent, _ := q.GetAgent(ctx, util.MustParseUUID(agentID))
	daily := 5.0
	seedBudget(t, ctx, q, workspaceID, util.UUIDToString(agent.RuntimeID), nil, &daily, nil, nil)
	seedSpend(t, ctx, pool, agentID, issueID, 5, time.Now().Add(-time.Minute))

	issue := db.Issue{
		ID: util.MustParseUUID(issueID), AssigneeID: util.MustParseUUID(agentID),
		Priority: "medium", CreatorType: "member", CreatorID: util.MustParseUUID(creatorID),
		WorkspaceID:  util.MustParseUUID(workspaceID),
		AssigneeType: pgtype.Text{String: "agent", Valid: true},
	}
	svc := &TaskService{Queries: q, TxStarter: pool, Bus: events.New()}
	_, err := svc.EnqueueTaskForIssue(ctx, issue)
	var exceeded *RuntimeBudgetExceededError
	if !errors.As(err, &exceeded) {
		t.Fatalf("expected budget refusal, got %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM agent_task_queue WHERE issue_id = $1 AND status = 'queued'`, issueID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("refused run must not be queued, found %d", n)
	}
}

// Direct chat and channel chat create their tasks through different helpers
// (SendDirectChatMessage vs enqueueChatTaskTx), so both need their own gate —
// the handler's budget_exceeded mapping covers the first one.
func TestChatEnqueuePathsRefusedWhenBudgetReached(t *testing.T) {
	pool := newResolveOriginatorPool(t)
	ctx := context.Background()
	q := db.New(pool)
	workspaceID, userID, agentID, issueID := seedAttributionFixture(t, pool)
	agent, _ := q.GetAgent(ctx, util.MustParseUUID(agentID))
	daily := 5.0
	seedBudget(t, ctx, q, workspaceID, util.UUIDToString(agent.RuntimeID), nil, &daily, nil, nil)
	seedSpend(t, ctx, pool, agentID, issueID, 5, time.Now().Add(-time.Minute))

	var chatSessionID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO chat_session (workspace_id, agent_id, creator_id)
		VALUES ($1, $2, $3) RETURNING id`, workspaceID, agentID, userID).Scan(&chatSessionID); err != nil {
		t.Fatalf("seed chat session: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM chat_session WHERE id = $1`, chatSessionID)
	})

	session := db.ChatSession{
		ID:          util.MustParseUUID(chatSessionID),
		WorkspaceID: util.MustParseUUID(workspaceID),
		AgentID:     util.MustParseUUID(agentID),
		CreatorID:   util.MustParseUUID(userID),
	}
	svc := &TaskService{Queries: q, TxStarter: pool, Bus: events.New()}

	var exceeded *RuntimeBudgetExceededError
	if _, err := svc.EnqueueChatTask(ctx, session, util.MustParseUUID(userID), false); !errors.As(err, &exceeded) {
		t.Fatalf("EnqueueChatTask: expected budget refusal, got %v", err)
	}
	if _, err := svc.SendDirectChatMessage(ctx, session, agent, util.MustParseUUID(userID), "hello", nil, "member", util.MustParseUUID(userID)); !errors.As(err, &exceeded) {
		t.Fatalf("SendDirectChatMessage: expected budget refusal, got %v", err)
	}

	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM agent_task_queue WHERE chat_session_id = $1`, chatSessionID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("refused chat runs must not be queued, found %d", n)
	}
}

func TestRuntimeBudgetNoticeFiresOncePerPeriod(t *testing.T) {
	pool := newResolveOriginatorPool(t)
	ctx := context.Background()
	q := db.New(pool)
	workspaceID, ownerID, agentID, issueID := seedAttributionFixture(t, pool)
	agent, _ := q.GetAgent(ctx, util.MustParseUUID(agentID))
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	daily := 5.0
	row := seedBudget(t, ctx, q, workspaceID, util.UUIDToString(agent.RuntimeID), nil, &daily, nil, nil)
	seedSpend(t, ctx, pool, agentID, issueID, 6, now.Add(-time.Hour))
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM inbox_item WHERE workspace_id = $1 AND type = 'runtime_budget_exceeded'`, workspaceID)
	})

	svc := &TaskService{Queries: q, TxStarter: pool, Bus: events.New()}
	for i := 0; i < 2; i++ {
		if err := svc.checkRuntimeCostBudget(ctx, q, agent, now); err == nil {
			t.Fatal("expected refusal")
		}
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM inbox_item WHERE workspace_id = $1 AND type = 'runtime_budget_exceeded' AND recipient_id = $2`, workspaceID, ownerID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("runtime owner notices = %d, want exactly 1", n)
	}
	// Next day: yesterday's spend is outside the new UTC day, so the check
	// passes until today's spend lands; then the marker no longer matches and
	// the notice fires again.
	if err := svc.checkRuntimeCostBudget(ctx, q, agent, now.Add(24*time.Hour)); err != nil {
		t.Fatalf("expected pass at the start of the next day, got %v", err)
	}
	seedSpend(t, ctx, pool, agentID, issueID, 6, now.Add(25*time.Hour))
	if err := svc.checkRuntimeCostBudget(ctx, q, agent, now.Add(26*time.Hour)); err == nil {
		t.Fatal("expected refusal on the next day")
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM inbox_item WHERE workspace_id = $1 AND type = 'runtime_budget_exceeded'`, workspaceID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("notices after period rollover = %d, want 2", n)
	}
	_ = row
}
