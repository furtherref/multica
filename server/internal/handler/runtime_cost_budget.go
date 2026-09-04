package handler

import (
	"encoding/json"
	"math"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	obsmetrics "github.com/multica-ai/multica/server/internal/metrics"
	"github.com/multica-ai/multica/server/internal/pricing"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

// Wire shapes for /api/runtimes/{id}/budget. Amounts are USD numbers; the
// database keeps ticks. A null period means unlimited.
type RuntimeBudgetPeriodResponse struct {
	LimitUSD    float64 `json:"limit_usd"`
	UsedUSD     float64 `json:"used_usd"`
	PeriodStart string  `json:"period_start"`
	ResetAt     string  `json:"reset_at"`
	Reached     bool    `json:"reached"`
}

type RuntimeBudgetScopeResponse struct {
	UserID  string                       `json:"user_id,omitempty"`
	Daily   *RuntimeBudgetPeriodResponse `json:"daily"`
	Weekly  *RuntimeBudgetPeriodResponse `json:"weekly"`
	Monthly *RuntimeBudgetPeriodResponse `json:"monthly"`
}

type RuntimeBudgetResponse struct {
	Runtime   *RuntimeBudgetScopeResponse  `json:"runtime"`
	Users     []RuntimeBudgetScopeResponse `json:"users"`
	CanManage bool                         `json:"can_manage"`
}

type runtimeBudgetScopeInput struct {
	UserID     string   `json:"user_id"`
	DailyUSD   *float64 `json:"daily_usd"`
	WeeklyUSD  *float64 `json:"weekly_usd"`
	MonthlyUSD *float64 `json:"monthly_usd"`
}

type runtimeBudgetPutRequest struct {
	Runtime *runtimeBudgetScopeInput  `json:"runtime"`
	Users   []runtimeBudgetScopeInput `json:"users"`
}

const maxBudgetUSD = 1_000_000

// zeroBudgetScopeUUID is the runtime-total scope key in
// DeleteRuntimeCostBudgetsExcept: the query coalesces a NULL user_id to the
// all-zero uuid so one uuid[] can name both kinds of scope.
var zeroBudgetScopeUUID = pgtype.UUID{Valid: true}

// canManageRuntimeBudget reports whether a member may set this runtime's cost
// budget. Runtime owner only, the same gate canSetRuntimeVisibility applies:
// the budget caps what the owner's own machine and credentials may spend, so
// it is the owner's call and not an administrative one. Workspace owners and
// admins get no override — an ownerless runtime (profile-created, or an owner
// since removed) therefore has no one who can set a budget and stays
// read-only.
func canManageRuntimeBudget(member db.Member, rt db.AgentRuntime) bool {
	return rt.OwnerID.Valid && uuidToString(rt.OwnerID) == uuidToString(member.UserID)
}

func periodResponse(p *service.RuntimeBudgetPeriodStatus) *RuntimeBudgetPeriodResponse {
	if p == nil {
		return nil
	}
	return &RuntimeBudgetPeriodResponse{
		LimitUSD:    pricing.TicksToUSD(p.LimitTicks),
		UsedUSD:     pricing.TicksToUSD(p.UsedTicks),
		PeriodStart: p.PeriodStart.UTC().Format(time.RFC3339),
		ResetAt:     p.ResetAt.UTC().Format(time.RFC3339),
		Reached:     p.Reached,
	}
}

func scopeResponse(sc service.RuntimeBudgetScopeStatus) RuntimeBudgetScopeResponse {
	out := RuntimeBudgetScopeResponse{
		Daily:   periodResponse(sc.Periods[pricing.PeriodDaily]),
		Weekly:  periodResponse(sc.Periods[pricing.PeriodWeekly]),
		Monthly: periodResponse(sc.Periods[pricing.PeriodMonthly]),
	}
	if sc.UserID.Valid {
		out.UserID = uuidToString(sc.UserID)
	}
	return out
}

func (h *Handler) writeRuntimeBudget(w http.ResponseWriter, r *http.Request, rt db.AgentRuntime, member db.Member) {
	status, err := h.TaskService.RuntimeCostBudgetStatus(r.Context(), rt.ID, time.Now())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load runtime budget")
		return
	}
	resp := RuntimeBudgetResponse{Users: []RuntimeBudgetScopeResponse{}, CanManage: canManageRuntimeBudget(member, rt)}
	if status.Runtime != nil {
		sc := scopeResponse(*status.Runtime)
		resp.Runtime = &sc
	}
	for _, u := range status.Users {
		resp.Users = append(resp.Users, scopeResponse(u))
	}
	writeJSON(w, http.StatusOK, resp)
}

// GetRuntimeCostBudget: anyone who can read the runtime sees every scope's
// limit and spend, the same visibility the Cost-by-owner tab already has.
func (h *Handler) GetRuntimeCostBudget(w http.ResponseWriter, r *http.Request) {
	rt, member, ok := h.requireRuntimeReadAccess(w, r, obsmetrics.RuntimeLookupSourceRuntimeAPI, chi.URLParam(r, "runtimeId"))
	if !ok {
		return
	}
	h.writeRuntimeBudget(w, r, rt, member)
}

// validBudgetAmount accepts a positive finite USD amount with at most two
// decimals and returns its ticks. nil means "no limit".
func validBudgetAmount(v *float64) (pgtype.Int8, bool) {
	if v == nil {
		return pgtype.Int8{}, true
	}
	usd := *v
	if math.IsNaN(usd) || math.IsInf(usd, 0) || usd <= 0 || usd > maxBudgetUSD {
		return pgtype.Int8{}, false
	}
	cents := usd * 100
	if math.Abs(cents-math.Round(cents)) > 1e-6 {
		return pgtype.Int8{}, false
	}
	return pgtype.Int8{Int64: pricing.USDToTicks(usd), Valid: true}, true
}

// PutRuntimeCostBudget replaces the whole budget set of a runtime. Runtime
// owner only (canManageRuntimeBudget): the budget caps the owner's own machine
// and credentials, so workspace owners and admins are excluded exactly as they
// are from changing the runtime's visibility. That gate is also what lets the
// response echo the GET body unconditionally: the owner always passes
// canUseRuntimeForAgent, so the reply never hands out spend the GET gate would
// deny. Widening the gate again would need that 204-without-body branch back.
func (h *Handler) PutRuntimeCostBudget(w http.ResponseWriter, r *http.Request) {
	runtimeID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "runtimeId"), "runtime_id")
	if !ok {
		return
	}
	rt, err := h.getAgentRuntime(r.Context(), obsmetrics.RuntimeLookupSourceRuntimeAPI, runtimeID)
	if err != nil {
		writeError(w, http.StatusNotFound, "runtime not found")
		return
	}
	member, ok := h.requireWorkspaceMember(w, r, uuidToString(rt.WorkspaceID), "runtime not found")
	if !ok {
		return
	}
	if !canManageRuntimeBudget(member, rt) {
		writeError(w, http.StatusForbidden, "only the runtime owner can set its cost budget")
		return
	}
	var req runtimeBudgetPutRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	type upsert struct {
		userID  pgtype.UUID
		daily   pgtype.Int8
		weekly  pgtype.Int8
		monthly pgtype.Int8
	}
	var writes []upsert
	// Never nil: a Go nil binds as SQL NULL, and `<> ALL(NULL)` deletes nothing.
	keep := []pgtype.UUID{}
	parseScope := func(in runtimeBudgetScopeInput, userID pgtype.UUID) bool {
		daily, ok1 := validBudgetAmount(in.DailyUSD)
		weekly, ok2 := validBudgetAmount(in.WeeklyUSD)
		monthly, ok3 := validBudgetAmount(in.MonthlyUSD)
		if !ok1 || !ok2 || !ok3 {
			writeError(w, http.StatusBadRequest, "budget amounts must be positive USD with at most two decimals, up to 1,000,000")
			return false
		}
		if !daily.Valid && !weekly.Valid && !monthly.Valid {
			return true // all empty: the scope is removed, nothing to keep
		}
		writes = append(writes, upsert{userID: userID, daily: daily, weekly: weekly, monthly: monthly})
		if userID.Valid {
			keep = append(keep, userID)
		} else {
			keep = append(keep, zeroBudgetScopeUUID)
		}
		return true
	}
	if req.Runtime != nil && !parseScope(*req.Runtime, pgtype.UUID{}) {
		return
	}
	seen := map[string]bool{}
	for _, u := range req.Users {
		userID, ok := parseUUIDOrBadRequest(w, u.UserID, "user_id")
		if !ok {
			return
		}
		if seen[u.UserID] {
			writeError(w, http.StatusBadRequest, "user_id listed more than once")
			return
		}
		seen[u.UserID] = true
		if _, err := h.getWorkspaceMember(r.Context(), u.UserID, uuidToString(rt.WorkspaceID)); err != nil {
			writeError(w, http.StatusBadRequest, "user_id is not a workspace member")
			return
		}
		if !parseScope(u, userID) {
			return
		}
	}

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save runtime budget")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)
	for _, wr := range writes {
		if _, err := qtx.UpsertRuntimeCostBudget(r.Context(), db.UpsertRuntimeCostBudgetParams{
			ID: dbid.NewV7(), WorkspaceID: rt.WorkspaceID, RuntimeID: rt.ID, UserID: wr.userID,
			DailyLimitUsdTicks: wr.daily, WeeklyLimitUsdTicks: wr.weekly, MonthlyLimitUsdTicks: wr.monthly,
			UpdatedBy: member.UserID,
		}); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to save runtime budget")
			return
		}
	}
	if err := qtx.DeleteRuntimeCostBudgetsExcept(r.Context(), db.DeleteRuntimeCostBudgetsExceptParams{
		RuntimeID: rt.ID, KeepKeys: keep,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save runtime budget")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save runtime budget")
		return
	}
	h.writeRuntimeBudget(w, r, rt, member)
}
