package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/pricing"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
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
			s.notifyRuntimeBudgetExceeded(ctx, q, row, exceeded)
			return exceeded
		}
	}
	return nil
}

// notifyRuntimeBudgetExceeded is implemented in Task 4. Until then it is a
// no-op so the check can ship on its own.
func (s *TaskService) notifyRuntimeBudgetExceeded(ctx context.Context, q *db.Queries, row db.RuntimeCostBudget, exceeded *RuntimeBudgetExceededError) {
}
