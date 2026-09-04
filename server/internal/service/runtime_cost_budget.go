package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/events"
	obsmetrics "github.com/multica-ai/multica/server/internal/metrics"
	"github.com/multica-ai/multica/server/internal/pricing"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// RuntimeBudgetScope names which row refused a run.
type RuntimeBudgetScope string

const (
	RuntimeBudgetScopeRuntime RuntimeBudgetScope = "runtime"
	RuntimeBudgetScopeUser    RuntimeBudgetScope = "user"
)

// RuntimeBudgetExceededError is returned by the enqueue helpers when a
// configured limit for the target runtime is already spent. Handlers map it to
// dispatch.ReasonBudgetExceeded with errors.As; it is never matched by string.
type RuntimeBudgetExceededError struct {
	Scope       RuntimeBudgetScope
	Period      pricing.Period
	RuntimeID   pgtype.UUID
	UserID      pgtype.UUID
	UsedTicks   int64
	LimitTicks  int64
	PeriodStart time.Time
	ResetAt     time.Time
}

func (e *RuntimeBudgetExceededError) Error() string {
	return fmt.Sprintf("runtime cost budget exceeded: %s %s limit %.2f USD, used %.2f USD, resets %s",
		e.Scope, e.Period, pricing.TicksToUSD(e.LimitTicks), pricing.TicksToUSD(e.UsedTicks), e.ResetAt.UTC().Format(time.RFC3339))
}

// PublicReason names the refusal without any amount. Use it everywhere the
// message is persisted or broadcast beyond the person who set the budget:
// agent_task_queue.error (which task:failed republishes to every workspace
// subscriber) and autopilot_run.failure_reason. GET /api/runtimes/{id}/budget
// is gated on runtime read access and PUT deliberately withholds spend from a
// writer who may not read a private runtime, so a limit or a running total
// carried in one of those strings would hand the figures to the whole
// workspace instead. Error() keeps the amounts for server logs and the
// recipient-scoped inbox notice keeps them for the two people entitled to see
// them.
func (e *RuntimeBudgetExceededError) PublicReason() string {
	return fmt.Sprintf("runtime cost budget reached (%s %s)", e.Scope, e.Period)
}

// budgetLimit returns the configured limit of one period on a row, or false.
func budgetLimit(row db.RuntimeCostBudget, p pricing.Period) (int64, bool) {
	var v pgtype.Int8
	switch p {
	case pricing.PeriodDaily:
		v = row.DailyLimitUsdTicks
	case pricing.PeriodWeekly:
		v = row.WeeklyLimitUsdTicks
	case pricing.PeriodMonthly:
		v = row.MonthlyLimitUsdTicks
	}
	return v.Int64, v.Valid
}

// runtimeSpend is the priced spend of one runtime, for every budget period,
// built once from a single ListRuntimeSpendByOwner query. total covers every
// task on the runtime including agents nobody owns; byOwner is keyed by the
// agent owner's uuid string and backs the per-user scopes.
type runtimeSpend struct {
	total   map[pricing.Period]int64
	byOwner map[pricing.Period]map[string]int64
}

// ticks returns the spend of one scope in one period. An invalid ownerUserID
// asks for the runtime total; a user with no spend reads as zero.
func (s runtimeSpend) ticks(p pricing.Period, ownerUserID pgtype.UUID) int64 {
	if !ownerUserID.Valid {
		return s.total[p]
	}
	return s.byOwner[p][util.UUIDToString(ownerUserID)]
}

// loadRuntimeSpend reads the spend of every scope and period of one runtime in
// one query, so a check or a status read costs one aggregate over task_usage
// instead of one per (budget row, period).
//
// The database returns raw sums per (owner, provider, model); pricing stays in
// Go so the rate table, not the query, decides what an uncosted token costs.
// Grouping by owner splits a provider/model bucket that the per-scope query
// used to sum whole, so the runtime total rounds the rate-table estimate once
// per owner rather than once overall — a sub-tick (1e-10 USD) difference on
// unpriced usage only, and provider-reported cost is unaffected.
func loadRuntimeSpend(ctx context.Context, q *db.Queries, runtimeID pgtype.UUID, now time.Time) (runtimeSpend, error) {
	spend := runtimeSpend{
		total:   make(map[pricing.Period]int64, len(pricing.AllPeriods)),
		byOwner: make(map[pricing.Period]map[string]int64, len(pricing.AllPeriods)),
	}
	for _, p := range pricing.AllPeriods {
		spend.byOwner[p] = map[string]int64{}
	}
	periodStart := func(p pricing.Period) pgtype.Timestamptz {
		return pgtype.Timestamptz{Time: pricing.PeriodStart(now, p), Valid: true}
	}
	rows, err := q.ListRuntimeSpendByOwner(ctx, db.ListRuntimeSpendByOwnerParams{
		RuntimeID:    runtimeID,
		DailyStart:   periodStart(pricing.PeriodDaily),
		WeeklyStart:  periodStart(pricing.PeriodWeekly),
		MonthlyStart: periodStart(pricing.PeriodMonthly),
	})
	if err != nil {
		return runtimeSpend{}, fmt.Errorf("list runtime spend: %w", err)
	}
	add := func(p pricing.Period, owner pgtype.UUID, ticks int64) {
		spend.total[p] += ticks
		if owner.Valid {
			spend.byOwner[p][util.UUIDToString(owner)] += ticks
		}
	}
	for _, r := range rows {
		add(pricing.PeriodDaily, r.OwnerID, pricing.EstimateCostTicks(r.Model, r.DailyCostUsdTicks,
			r.DailyUncostedInputTokens, r.DailyUncostedOutputTokens, r.DailyUncostedCacheReadTokens, r.DailyUncostedCacheWriteTokens))
		add(pricing.PeriodWeekly, r.OwnerID, pricing.EstimateCostTicks(r.Model, r.WeeklyCostUsdTicks,
			r.WeeklyUncostedInputTokens, r.WeeklyUncostedOutputTokens, r.WeeklyUncostedCacheReadTokens, r.WeeklyUncostedCacheWriteTokens))
		add(pricing.PeriodMonthly, r.OwnerID, pricing.EstimateCostTicks(r.Model, r.MonthlyCostUsdTicks,
			r.MonthlyUncostedInputTokens, r.MonthlyUncostedOutputTokens, r.MonthlyUncostedCacheReadTokens, r.MonthlyUncostedCacheWriteTokens))
	}
	return spend, nil
}

// evaluateBudgetRow checks every configured period of one row against the
// already-loaded spend and returns the first reached limit.
func evaluateBudgetRow(row db.RuntimeCostBudget, scope RuntimeBudgetScope, spend runtimeSpend, now time.Time) *RuntimeBudgetExceededError {
	for _, p := range pricing.AllPeriods {
		limit, ok := budgetLimit(row, p)
		if !ok {
			continue
		}
		used := spend.ticks(p, row.UserID)
		if used >= limit {
			return &RuntimeBudgetExceededError{
				Scope: scope, Period: p, RuntimeID: row.RuntimeID, UserID: row.UserID,
				UsedTicks: used, LimitTicks: limit, PeriodStart: pricing.PeriodStart(now, p),
				ResetAt: pricing.NextPeriodStart(now, p),
			}
		}
	}
	return nil
}

// checkRuntimeCostBudget refuses the enqueue when the agent's runtime total or
// the agent owner's per-user budget is spent for the current UTC period. It
// runs after attribution in every enqueue helper. Workspaces without budgets
// pay one indexed lookup and return immediately; a runtime whose only budgets
// belong to other owners costs the same, because the spend query is issued
// only once a row that applies to this agent is known. A database error is
// returned as-is so the enqueue fails closed rather than silently spending.
func (s *TaskService) checkRuntimeCostBudget(ctx context.Context, q *db.Queries, agent db.Agent, now time.Time) error {
	if !agent.RuntimeID.Valid {
		return nil
	}
	rows, err := q.ListRuntimeCostBudgets(ctx, agent.RuntimeID)
	if err != nil {
		return fmt.Errorf("list runtime cost budgets: %w", err)
	}
	if len(rows) == 0 {
		return nil
	}
	type applicableRow struct {
		row   db.RuntimeCostBudget
		scope RuntimeBudgetScope
	}
	applicable := make([]applicableRow, 0, len(rows))
	for _, row := range rows {
		scope := RuntimeBudgetScopeUser
		if !row.UserID.Valid {
			scope = RuntimeBudgetScopeRuntime
		} else if !agent.OwnerID.Valid || util.UUIDToString(row.UserID) != util.UUIDToString(agent.OwnerID) {
			continue
		}
		applicable = append(applicable, applicableRow{row: row, scope: scope})
	}
	if len(applicable) == 0 {
		return nil
	}
	spend, err := loadRuntimeSpend(ctx, q, agent.RuntimeID, now)
	if err != nil {
		return err
	}
	for _, a := range applicable {
		exceeded := evaluateBudgetRow(a.row, a.scope, spend, now)
		if exceeded == nil {
			continue
		}
		slog.Info("task enqueue refused: runtime cost budget reached",
			"runtime_id", util.UUIDToString(a.row.RuntimeID), "scope", a.scope, "period", exceeded.Period,
			"used_ticks", exceeded.UsedTicks, "limit_ticks", exceeded.LimitTicks)
		// s.Queries, never q: q may be the caller's transaction, and the
		// refusal returned below always rolls that transaction back.
		s.notifyRuntimeBudgetExceeded(ctx, s.Queries, a.row, exceeded, agent.OwnerID)
		return exceeded
	}
	return nil
}

// notifyRuntimeBudgetExceeded creates one "limit reached" inbox item per
// period for the scope that refused the run. Recipients: the owner of the
// agent whose run was refused (blockedOwnerID) and the runtime owner,
// de-duplicated. Both scopes use that same set. Keying the first recipient on
// exceeded.UserID instead would notify nobody but the runtime owner for a
// runtime-total refusal, because that scope's row has no user_id — and the
// person whose run was refused is exactly who needs to know. The recipient set
// and notice details are built first, and MarkRuntimeCostBudgetNotified only
// claims the period once a recipient is known — an empty recipient set (a
// runtime-scope budget on a runtime with no owner) or a lookup/marshal
// failure must not burn the claim, or a later refusal that could resolve a
// recipient would never retry. Once claimed, concurrent refusals produce one
// notice. Failures are logged; the refusal itself is already decided.
//
// It must NOT use the caller's transaction. checkRuntimeCostBudget refuses the
// enqueue right after calling this, and on the chat paths that refusal rolls the
// enclosing transaction back — a notification or MarkRuntimeCostBudgetNotified
// marker written on that handle would be discarded with it. Callers pass the
// auto-commit s.Queries, and the implementation must keep using the handle it is
// given rather than reaching for a transaction of the enqueue.
func (s *TaskService) notifyRuntimeBudgetExceeded(ctx context.Context, q *db.Queries, row db.RuntimeCostBudget, exceeded *RuntimeBudgetExceededError, blockedOwnerID pgtype.UUID) {
	rt, err := RuntimeLookup{Queries: q, Metrics: s.Metrics, Source: obsmetrics.RuntimeLookupSourceBudgetNotice}.Get(ctx, row.RuntimeID)
	if err != nil {
		slog.Warn("runtime budget notice: load runtime failed", "runtime_id", util.UUIDToString(row.RuntimeID), "error", err)
		return
	}
	recipients := map[string]pgtype.UUID{}
	if blockedOwnerID.Valid {
		recipients[util.UUIDToString(blockedOwnerID)] = blockedOwnerID
	}
	if rt.OwnerID.Valid {
		recipients[util.UUIDToString(rt.OwnerID)] = rt.OwnerID
	}
	if len(recipients) == 0 {
		slog.Debug("runtime budget notice: no recipients, not claiming period",
			"runtime_id", util.UUIDToString(row.RuntimeID), "scope", exceeded.Scope, "period", exceeded.Period)
		return
	}
	// inbox_item.details is a flat string map on the wire (InboxItem.details is
	// Record<string, string> in packages/core/types/inbox.ts), so amounts are
	// formatted here and an absent user_id is omitted rather than sent as null.
	detailFields := map[string]string{
		"scope":        string(exceeded.Scope),
		"period":       string(exceeded.Period),
		"runtime_id":   util.UUIDToString(row.RuntimeID),
		"used_usd":     strconv.FormatFloat(pricing.TicksToUSD(exceeded.UsedTicks), 'f', 2, 64),
		"limit_usd":    strconv.FormatFloat(pricing.TicksToUSD(exceeded.LimitTicks), 'f', 2, 64),
		"period_start": exceeded.PeriodStart.UTC().Format(time.RFC3339),
		"reset_at":     exceeded.ResetAt.UTC().Format(time.RFC3339),
	}
	if exceeded.UserID.Valid {
		detailFields["user_id"] = util.UUIDToString(exceeded.UserID)
	}
	details, err := json.Marshal(detailFields)
	if err != nil {
		slog.Warn("runtime budget notice: marshal details failed", "error", err)
		return
	}
	claimed, err := q.MarkRuntimeCostBudgetNotified(ctx, db.MarkRuntimeCostBudgetNotifiedParams{
		Period:      string(exceeded.Period),
		PeriodStart: pgtype.Timestamptz{Time: exceeded.PeriodStart, Valid: true},
		ID:          row.ID,
	})
	if err != nil {
		slog.Warn("runtime budget notice claim failed", "budget_id", util.UUIDToString(row.ID), "error", err)
		return
	}
	if claimed == 0 {
		return
	}
	scopeLabel := "This runtime"
	if exceeded.Scope == RuntimeBudgetScopeUser {
		scopeLabel = "Your agents on this runtime"
	}
	body := fmt.Sprintf("%s reached the %s cost limit of $%.2f (used $%.2f). New runs are refused until %s UTC.",
		scopeLabel, exceeded.Period, pricing.TicksToUSD(exceeded.LimitTicks), pricing.TicksToUSD(exceeded.UsedTicks),
		exceeded.ResetAt.UTC().Format("Jan 2, 15:04"))
	for _, recipient := range recipients {
		item, err := q.CreateInboxItem(ctx, db.CreateInboxItemParams{
			ID: dbid.NewV7(), WorkspaceID: row.WorkspaceID,
			RecipientType: "member", RecipientID: recipient,
			Type: "runtime_budget_exceeded", Severity: "attention", IssueID: pgtype.UUID{},
			Title: "Runtime cost budget reached", Body: pgtype.Text{String: body, Valid: true},
			ActorType: pgtype.Text{String: "system", Valid: true}, ActorID: pgtype.UUID{},
			Details: details,
		})
		if err != nil {
			slog.Warn("runtime budget notice: create inbox item failed", "error", err)
			continue
		}
		if s.Bus != nil {
			s.Bus.Publish(events.Event{
				Type: protocol.EventInboxNew, WorkspaceID: util.UUIDToString(item.WorkspaceID), ActorType: "system",
				Payload: map[string]any{"item": map[string]any{
					"id": util.UUIDToString(item.ID), "workspace_id": util.UUIDToString(item.WorkspaceID),
					"recipient_type": item.RecipientType, "recipient_id": util.UUIDToString(item.RecipientID),
					"type": item.Type, "severity": item.Severity, "issue_id": nil,
					"issue_status": nil, "issue_priority": nil,
					"title": item.Title, "body": util.TextToPtr(item.Body), "read": item.Read,
					"archived": item.Archived, "created_at": util.TimestampToString(item.CreatedAt),
					"actor_type": util.TextToPtr(item.ActorType), "actor_id": nil,
					"details": json.RawMessage(item.Details),
				}},
			})
		}
	}
}

// RuntimeBudgetPeriodStatus is one configured period of one scope.
type RuntimeBudgetPeriodStatus struct {
	LimitTicks  int64
	UsedTicks   int64
	PeriodStart time.Time
	ResetAt     time.Time
	Reached     bool
}

// RuntimeBudgetScopeStatus is the runtime total (UserID invalid) or one user.
type RuntimeBudgetScopeStatus struct {
	UserID  pgtype.UUID
	Periods map[pricing.Period]*RuntimeBudgetPeriodStatus
}

// RuntimeBudgetStatus is the read model behind GET /api/runtimes/{id}/budget.
type RuntimeBudgetStatus struct {
	Runtime *RuntimeBudgetScopeStatus
	Users   []RuntimeBudgetScopeStatus
}

// scopeStatus reports every period of one budget row from the already-loaded
// spend. A period with no configured limit maps to a nil entry, which the API
// renders as null.
func scopeStatus(row db.RuntimeCostBudget, spend runtimeSpend, now time.Time) RuntimeBudgetScopeStatus {
	out := RuntimeBudgetScopeStatus{UserID: row.UserID, Periods: map[pricing.Period]*RuntimeBudgetPeriodStatus{}}
	for _, p := range pricing.AllPeriods {
		limit, ok := budgetLimit(row, p)
		if !ok {
			out.Periods[p] = nil
			continue
		}
		used := spend.ticks(p, row.UserID)
		out.Periods[p] = &RuntimeBudgetPeriodStatus{
			LimitTicks: limit, UsedTicks: used, PeriodStart: pricing.PeriodStart(now, p),
			ResetAt: pricing.NextPeriodStart(now, p), Reached: used >= limit,
		}
	}
	return out
}

// RuntimeCostBudgetStatus loads every budget row of a runtime with its
// current-period spend. Spend is computed on demand, never stored: one grouped
// query covers every scope and period, so the cost does not grow with the
// number of per-user rows.
func (s *TaskService) RuntimeCostBudgetStatus(ctx context.Context, runtimeID pgtype.UUID, now time.Time) (RuntimeBudgetStatus, error) {
	rows, err := s.Queries.ListRuntimeCostBudgets(ctx, runtimeID)
	if err != nil {
		return RuntimeBudgetStatus{}, fmt.Errorf("list runtime cost budgets: %w", err)
	}
	status := RuntimeBudgetStatus{Users: []RuntimeBudgetScopeStatus{}}
	if len(rows) == 0 {
		return status, nil
	}
	spend, err := loadRuntimeSpend(ctx, s.Queries, runtimeID, now)
	if err != nil {
		return RuntimeBudgetStatus{}, err
	}
	for _, row := range rows {
		sc := scopeStatus(row, spend, now)
		if row.UserID.Valid {
			status.Users = append(status.Users, sc)
		} else {
			total := sc
			status.Runtime = &total
		}
	}
	return status, nil
}
