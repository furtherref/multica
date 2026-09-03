package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
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

// runtimeSpendTicks sums the priced spend of one scope since `since`.
// ownerUserID invalid means the runtime total.
func runtimeSpendTicks(ctx context.Context, q *db.Queries, runtimeID, ownerUserID pgtype.UUID, since time.Time) (int64, error) {
	rows, err := q.ListRuntimeSpendSince(ctx, db.ListRuntimeSpendSinceParams{
		RuntimeID:   runtimeID,
		Since:       pgtype.Timestamptz{Time: since, Valid: true},
		OwnerUserID: ownerUserID,
	})
	if err != nil {
		return 0, fmt.Errorf("list runtime spend: %w", err)
	}
	var total int64
	for _, r := range rows {
		total += pricing.EstimateCostTicks(r.Model, r.CostUsdTicks,
			r.UncostedInputTokens, r.UncostedOutputTokens, r.UncostedCacheReadTokens, r.UncostedCacheWriteTokens)
	}
	return total, nil
}

// evaluateBudgetRow checks every configured period of one row and returns the
// first reached limit.
func evaluateBudgetRow(ctx context.Context, q *db.Queries, row db.RuntimeCostBudget, scope RuntimeBudgetScope, now time.Time) (*RuntimeBudgetExceededError, error) {
	for _, p := range pricing.AllPeriods {
		limit, ok := budgetLimit(row, p)
		if !ok {
			continue
		}
		start := pricing.PeriodStart(now, p)
		used, err := runtimeSpendTicks(ctx, q, row.RuntimeID, row.UserID, start)
		if err != nil {
			return nil, err
		}
		if used >= limit {
			return &RuntimeBudgetExceededError{
				Scope: scope, Period: p, RuntimeID: row.RuntimeID, UserID: row.UserID,
				UsedTicks: used, LimitTicks: limit, PeriodStart: start, ResetAt: pricing.NextPeriodStart(now, p),
			}, nil
		}
	}
	return nil, nil
}

// checkRuntimeCostBudget refuses the enqueue when the agent's runtime total or
// the agent owner's per-user budget is spent for the current UTC period. It
// runs after attribution in every enqueue helper. Workspaces without budgets
// pay one indexed lookup and return immediately. A database error is returned
// as-is so the enqueue fails closed rather than silently spending.
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
	for _, row := range rows {
		scope := RuntimeBudgetScopeUser
		if !row.UserID.Valid {
			scope = RuntimeBudgetScopeRuntime
		} else if !agent.OwnerID.Valid || util.UUIDToString(row.UserID) != util.UUIDToString(agent.OwnerID) {
			continue
		}
		exceeded, err := evaluateBudgetRow(ctx, q, row, scope, now)
		if err != nil {
			return err
		}
		if exceeded != nil {
			slog.Info("task enqueue refused: runtime cost budget reached",
				"runtime_id", util.UUIDToString(row.RuntimeID), "scope", scope, "period", exceeded.Period,
				"used_ticks", exceeded.UsedTicks, "limit_ticks", exceeded.LimitTicks)
			// s.Queries, never q: q may be the caller's transaction, and the
			// refusal returned below always rolls that transaction back.
			s.notifyRuntimeBudgetExceeded(ctx, s.Queries, row, exceeded)
			return exceeded
		}
	}
	return nil
}

// notifyRuntimeBudgetExceeded creates one "limit reached" inbox item per
// period for the scope that refused the run. Recipients: the blocked user
// (per-user scope) and the runtime owner, de-duplicated. The recipient set
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
func (s *TaskService) notifyRuntimeBudgetExceeded(ctx context.Context, q *db.Queries, row db.RuntimeCostBudget, exceeded *RuntimeBudgetExceededError) {
	rt, err := RuntimeLookup{Queries: q, Metrics: s.Metrics, Source: obsmetrics.RuntimeLookupSourceBudgetNotice}.Get(ctx, row.RuntimeID)
	if err != nil {
		slog.Warn("runtime budget notice: load runtime failed", "runtime_id", util.UUIDToString(row.RuntimeID), "error", err)
		return
	}
	recipients := map[string]pgtype.UUID{}
	if exceeded.UserID.Valid {
		recipients[util.UUIDToString(exceeded.UserID)] = exceeded.UserID
	}
	if rt.OwnerID.Valid {
		recipients[util.UUIDToString(rt.OwnerID)] = rt.OwnerID
	}
	if len(recipients) == 0 {
		slog.Debug("runtime budget notice: no recipients, not claiming period",
			"runtime_id", util.UUIDToString(row.RuntimeID), "scope", exceeded.Scope, "period", exceeded.Period)
		return
	}
	var userID *string
	if exceeded.UserID.Valid {
		v := util.UUIDToString(exceeded.UserID)
		userID = &v
	}
	details, err := json.Marshal(map[string]any{
		"scope": exceeded.Scope, "period": exceeded.Period,
		"runtime_id": util.UUIDToString(row.RuntimeID), "user_id": userID,
		"used_usd": pricing.TicksToUSD(exceeded.UsedTicks), "limit_usd": pricing.TicksToUSD(exceeded.LimitTicks),
		"period_start": exceeded.PeriodStart.UTC().Format(time.RFC3339), "reset_at": exceeded.ResetAt.UTC().Format(time.RFC3339),
	})
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

// scopeStatus reports every period of one budget row. A period with no
// configured limit maps to a nil entry, which the API renders as null.
func scopeStatus(ctx context.Context, q *db.Queries, row db.RuntimeCostBudget, now time.Time) (RuntimeBudgetScopeStatus, error) {
	out := RuntimeBudgetScopeStatus{UserID: row.UserID, Periods: map[pricing.Period]*RuntimeBudgetPeriodStatus{}}
	for _, p := range pricing.AllPeriods {
		limit, ok := budgetLimit(row, p)
		if !ok {
			out.Periods[p] = nil
			continue
		}
		start := pricing.PeriodStart(now, p)
		used, err := runtimeSpendTicks(ctx, q, row.RuntimeID, row.UserID, start)
		if err != nil {
			return out, err
		}
		out.Periods[p] = &RuntimeBudgetPeriodStatus{
			LimitTicks: limit, UsedTicks: used, PeriodStart: start,
			ResetAt: pricing.NextPeriodStart(now, p), Reached: used >= limit,
		}
	}
	return out, nil
}

// RuntimeCostBudgetStatus loads every budget row of a runtime with its
// current-period spend. Spend is computed on demand, never stored.
func (s *TaskService) RuntimeCostBudgetStatus(ctx context.Context, runtimeID pgtype.UUID, now time.Time) (RuntimeBudgetStatus, error) {
	rows, err := s.Queries.ListRuntimeCostBudgets(ctx, runtimeID)
	if err != nil {
		return RuntimeBudgetStatus{}, fmt.Errorf("list runtime cost budgets: %w", err)
	}
	status := RuntimeBudgetStatus{Users: []RuntimeBudgetScopeStatus{}}
	for _, row := range rows {
		sc, err := scopeStatus(ctx, s.Queries, row, now)
		if err != nil {
			return RuntimeBudgetStatus{}, err
		}
		if row.UserID.Valid {
			status.Users = append(status.Users, sc)
		} else {
			total := sc
			status.Runtime = &total
		}
	}
	return status, nil
}
