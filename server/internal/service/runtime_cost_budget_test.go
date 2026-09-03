package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/pricing"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
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

// TestRuntimeBudgetNoticeReachesBlockedUserAndOwnerOnce covers the per-user
// scope where the blocked user IS the runtime owner: the recipient map must
// de-dup to one inbox row, the details must record the user scope, and the
// Bus must publish exactly one inbox:new event for it.
func TestRuntimeBudgetNoticeReachesBlockedUserAndOwnerOnce(t *testing.T) {
	pool := newResolveOriginatorPool(t)
	ctx := context.Background()
	q := db.New(pool)
	workspaceID, ownerID, agentID, issueID := seedAttributionFixture(t, pool)
	agent, _ := q.GetAgent(ctx, util.MustParseUUID(agentID))
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	weekly := 5.0
	seedBudget(t, ctx, q, workspaceID, util.UUIDToString(agent.RuntimeID), &ownerID, nil, &weekly, nil)
	seedSpend(t, ctx, pool, agentID, issueID, 6, now.Add(-time.Hour))
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM inbox_item WHERE workspace_id = $1 AND type = 'runtime_budget_exceeded'`, workspaceID)
	})

	var published []events.Event
	bus := events.New()
	bus.Subscribe(protocol.EventInboxNew, func(e events.Event) { published = append(published, e) })

	svc := &TaskService{Queries: q, TxStarter: pool, Bus: bus}
	var exceeded *RuntimeBudgetExceededError
	if err := svc.checkRuntimeCostBudget(ctx, q, agent, now); !errors.As(err, &exceeded) || exceeded.Scope != RuntimeBudgetScopeUser {
		t.Fatalf("expected user-scope refusal, got %v", err)
	}

	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM inbox_item WHERE workspace_id = $1 AND type = 'runtime_budget_exceeded' AND recipient_id = $2`, workspaceID, ownerID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("blocked-user/owner notices = %d, want exactly 1 (recipients must de-dup)", n)
	}

	var scope, userIDDetail string
	if err := pool.QueryRow(ctx, `SELECT details->>'scope', details->>'user_id' FROM inbox_item WHERE workspace_id = $1 AND type = 'runtime_budget_exceeded' AND recipient_id = $2`, workspaceID, ownerID).Scan(&scope, &userIDDetail); err != nil {
		t.Fatal(err)
	}
	if scope != "user" {
		t.Fatalf("details.scope = %q, want %q", scope, "user")
	}
	if userIDDetail != ownerID {
		t.Fatalf("details.user_id = %q, want %q", userIDDetail, ownerID)
	}

	if len(published) != 1 {
		t.Fatalf("published inbox:new events = %d, want 1", len(published))
	}
	payload, ok := published[0].Payload.(map[string]any)
	if !ok {
		t.Fatalf("event payload = %#v, want map[string]any", published[0].Payload)
	}
	item, ok := payload["item"].(map[string]any)
	if !ok {
		t.Fatalf("event payload missing item: %#v", payload)
	}
	if item["type"] != "runtime_budget_exceeded" {
		t.Fatalf("event item type = %v, want runtime_budget_exceeded", item["type"])
	}
}

// seedSecondOwnerAgent adds a second workspace member and an agent they own on
// runtimeID, so a refusal can name a blocked person who is NOT the runtime
// owner. Returns the new user's and agent's ids.
func seedSecondOwnerAgent(t *testing.T, ctx context.Context, pool *pgxpool.Pool, workspaceID, runtimeID string) (string, string) {
	t.Helper()
	var userID string
	if err := pool.QueryRow(ctx, `INSERT INTO "user" (name, email) VALUES ('Attr User 2', $1) RETURNING id`,
		fmt.Sprintf("attr2-%d@multica.test", time.Now().UnixNano())).Scan(&userID); err != nil {
		t.Fatalf("seed second user: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1`, userID) })
	if _, err := pool.Exec(ctx, `INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'member')`,
		workspaceID, userID); err != nil {
		t.Fatalf("seed second member: %v", err)
	}
	var agentID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO agent (workspace_id, name, runtime_mode, runtime_config, runtime_id, visibility,
			max_concurrent_tasks, owner_id, instructions, custom_env, custom_args)
		VALUES ($1, 'attr-agent-2', 'cloud', '{}'::jsonb, $2, 'workspace', 1, $3, '', '{}'::jsonb, '[]'::jsonb)
		RETURNING id`, workspaceID, runtimeID, userID).Scan(&agentID); err != nil {
		t.Fatalf("seed second agent: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, agentID) })
	return userID, agentID
}

// TestRuntimeBudgetNoticeReachesDistinctBlockedUserAndOwner covers the case
// where the blocked user and the runtime owner are different people: both
// must be notified, as two separate inbox rows.
func TestRuntimeBudgetNoticeReachesDistinctBlockedUserAndOwner(t *testing.T) {
	pool := newResolveOriginatorPool(t)
	ctx := context.Background()
	q := db.New(pool)
	workspaceID, ownerID, firstAgentID, issueID := seedAttributionFixture(t, pool)
	firstAgent, err := q.GetAgent(ctx, util.MustParseUUID(firstAgentID))
	if err != nil {
		t.Fatal(err)
	}
	runtimeID := util.UUIDToString(firstAgent.RuntimeID)
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)

	secondUserID, secondAgentID := seedSecondOwnerAgent(t, ctx, pool, workspaceID, runtimeID)

	daily := 5.0
	seedBudget(t, ctx, q, workspaceID, runtimeID, &secondUserID, &daily, nil, nil)
	seedSpend(t, ctx, pool, secondAgentID, issueID, 6, now.Add(-time.Hour))
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM inbox_item WHERE workspace_id = $1 AND type = 'runtime_budget_exceeded'`, workspaceID)
	})

	secondAgent, err := q.GetAgent(ctx, util.MustParseUUID(secondAgentID))
	if err != nil {
		t.Fatal(err)
	}
	svc := &TaskService{Queries: q, TxStarter: pool, Bus: events.New()}
	var exceeded *RuntimeBudgetExceededError
	if err := svc.checkRuntimeCostBudget(ctx, q, secondAgent, now); !errors.As(err, &exceeded) || exceeded.Scope != RuntimeBudgetScopeUser {
		t.Fatalf("expected user-scope refusal, got %v", err)
	}

	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM inbox_item WHERE workspace_id = $1 AND type = 'runtime_budget_exceeded' AND recipient_id = $2`, workspaceID, secondUserID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("blocked-user notices = %d, want exactly 1", n)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM inbox_item WHERE workspace_id = $1 AND type = 'runtime_budget_exceeded' AND recipient_id = $2`, workspaceID, ownerID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("runtime owner notices = %d, want exactly 1", n)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM inbox_item WHERE workspace_id = $1 AND type = 'runtime_budget_exceeded'`, workspaceID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("total notices = %d, want 2 (blocked user + runtime owner)", n)
	}
}

// TestRuntimeBudgetNoticeRuntimeScopeReachesBlockedOwnerAndRuntimeOwner is the
// regression for a runtime-TOTAL refusal: that row has no user_id, so a
// recipient set keyed on the exceeded scope's user id would notify only the
// runtime owner and leave the person whose run was actually refused with
// nothing. The blocked agent's owner is the first recipient in both scopes.
func TestRuntimeBudgetNoticeRuntimeScopeReachesBlockedOwnerAndRuntimeOwner(t *testing.T) {
	pool := newResolveOriginatorPool(t)
	ctx := context.Background()
	q := db.New(pool)
	workspaceID, ownerID, firstAgentID, issueID := seedAttributionFixture(t, pool)
	firstAgent, err := q.GetAgent(ctx, util.MustParseUUID(firstAgentID))
	if err != nil {
		t.Fatal(err)
	}
	runtimeID := util.UUIDToString(firstAgent.RuntimeID)
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)

	secondUserID, secondAgentID := seedSecondOwnerAgent(t, ctx, pool, workspaceID, runtimeID)

	// Runtime total: nil user id, so it blocks every agent on the runtime.
	daily := 5.0
	seedBudget(t, ctx, q, workspaceID, runtimeID, nil, &daily, nil, nil)
	seedSpend(t, ctx, pool, secondAgentID, issueID, 6, now.Add(-time.Hour))
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM inbox_item WHERE workspace_id = $1 AND type = 'runtime_budget_exceeded'`, workspaceID)
	})

	secondAgent, err := q.GetAgent(ctx, util.MustParseUUID(secondAgentID))
	if err != nil {
		t.Fatal(err)
	}
	svc := &TaskService{Queries: q, TxStarter: pool, Bus: events.New()}
	var exceeded *RuntimeBudgetExceededError
	if err := svc.checkRuntimeCostBudget(ctx, q, secondAgent, now); !errors.As(err, &exceeded) || exceeded.Scope != RuntimeBudgetScopeRuntime {
		t.Fatalf("expected runtime-scope refusal, got %v", err)
	}

	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM inbox_item WHERE workspace_id = $1 AND type = 'runtime_budget_exceeded' AND recipient_id = $2`, workspaceID, secondUserID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("blocked agent owner notices = %d, want exactly 1", n)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM inbox_item WHERE workspace_id = $1 AND type = 'runtime_budget_exceeded' AND recipient_id = $2`, workspaceID, ownerID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("runtime owner notices = %d, want exactly 1", n)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM inbox_item WHERE workspace_id = $1 AND type = 'runtime_budget_exceeded'`, workspaceID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("total notices = %d, want 2 (blocked agent owner + runtime owner)", n)
	}

	// The runtime scope has no user, so details must omit user_id entirely
	// rather than carry a JSON null: inbox details is a flat string map.
	var raw []byte
	if err := pool.QueryRow(ctx, `SELECT details FROM inbox_item WHERE workspace_id = $1 AND type = 'runtime_budget_exceeded' AND recipient_id = $2`, workspaceID, secondUserID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var details map[string]any
	if err := json.Unmarshal(raw, &details); err != nil {
		t.Fatalf("details is not an object: %v (%s)", err, raw)
	}
	if _, present := details["user_id"]; present {
		t.Fatalf("runtime-scope details carried user_id: %s", raw)
	}
	for _, key := range []string{"scope", "period", "runtime_id", "used_usd", "limit_usd", "period_start", "reset_at"} {
		if _, ok := details[key].(string); !ok {
			t.Fatalf("details.%s = %#v, want a string (InboxItem.details is Record<string, string>)", key, details[key])
		}
	}
	if details["used_usd"] != "6.00" || details["limit_usd"] != "5.00" {
		t.Fatalf("details amounts = %#v / %#v, want \"6.00\" / \"5.00\"", details["used_usd"], details["limit_usd"])
	}
}
