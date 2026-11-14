package handler

import (
	"context"
	"net/http"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
)

func budgetRequest(t *testing.T, userID, method, runtimeID string, body any) *http.Request {
	t.Helper()
	req := newRequest(method, "/api/runtimes/"+runtimeID+"/budget", body)
	req.Header.Set("X-User-ID", userID)
	return withURLParam(req, "runtimeId", runtimeID)
}

// The budget caps the runtime owner's own machine and credentials, so only
// they may set it — workspace owners and admins have no override, the same
// rule that gates runtime visibility. An ownerless runtime has nobody who
// qualifies and stays read-only for everyone.
func TestPutRuntimeCostBudgetRequiresRuntimeOwner(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ownerUserID := dbfx.User(t, "Budget Runtime Owner", "budget-rt-owner@example.com")
	dbfx.Member(t, testWorkspaceID, ownerUserID, "member")
	adminUserID := dbfx.User(t, "Budget Admin", "budget-admin@example.com")
	dbfx.Member(t, testWorkspaceID, adminUserID, "admin")
	runtimeID := dbfx.Runtime(t, "budget-rt", testutil.Cols{"visibility": "public", "owner_id": ownerUserID})
	t.Cleanup(func() { dbfx.Exec(t, `DELETE FROM runtime_cost_budget WHERE runtime_id = $1`, runtimeID) })
	body := map[string]any{"runtime": map[string]any{"daily_usd": 20}, "users": []any{}}

	// testUserID is the workspace owner; neither they nor the admin own this
	// runtime, so both are refused.
	testutil.Call(t, testHandler.PutRuntimeCostBudget, budgetRequest(t, adminUserID, http.MethodPut, runtimeID, body)).Want(http.StatusForbidden)
	testutil.Call(t, testHandler.PutRuntimeCostBudget, budgetRequest(t, testUserID, http.MethodPut, runtimeID, body)).Want(http.StatusForbidden)

	// The runtime owner is a plain workspace member and still writes it.
	var out map[string]any
	testutil.Call(t, testHandler.PutRuntimeCostBudget, budgetRequest(t, ownerUserID, http.MethodPut, runtimeID, body)).Want(http.StatusOK).JSON(&out)
	rt := out["runtime"].(map[string]any)
	if rt["daily"].(map[string]any)["limit_usd"].(float64) != 20 {
		t.Fatalf("runtime.daily = %#v", rt["daily"])
	}
	if rt["weekly"] != nil || rt["monthly"] != nil {
		t.Fatalf("unset periods must be null, got %#v", rt)
	}
	if out["can_manage"] != true {
		t.Fatalf("can_manage = %#v", out["can_manage"])
	}

	// An ownerless runtime is read-only, including for the workspace owner.
	ownerlessID := dbfx.Runtime(t, "budget-rt-ownerless", testutil.Cols{"visibility": "public", "owner_id": nil})
	testutil.Call(t, testHandler.PutRuntimeCostBudget, budgetRequest(t, testUserID, http.MethodPut, ownerlessID, body)).Want(http.StatusForbidden)
	testutil.Call(t, testHandler.PutRuntimeCostBudget, budgetRequest(t, adminUserID, http.MethodPut, ownerlessID, body)).Want(http.StatusForbidden)
	testutil.Call(t, testHandler.PutRuntimeCostBudget, budgetRequest(t, ownerUserID, http.MethodPut, ownerlessID, body)).Want(http.StatusForbidden)
	if n := dbfx.Count(t, `SELECT count(*) FROM runtime_cost_budget WHERE runtime_id = $1`, ownerlessID); n != 0 {
		t.Fatalf("ownerless runtime got %d budget rows, want 0", n)
	}
}

func TestPutRuntimeCostBudgetValidatesAmountsAndMembers(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	runtimeID := dbfx.Runtime(t, "budget-rt-validate", testutil.Cols{"visibility": "public"})
	cases := []struct {
		name string
		body map[string]any
	}{
		{"zero", map[string]any{"runtime": map[string]any{"daily_usd": 0}}},
		{"negative", map[string]any{"runtime": map[string]any{"daily_usd": -1}}},
		{"too large", map[string]any{"runtime": map[string]any{"daily_usd": 1000001}}},
		{"three decimals", map[string]any{"runtime": map[string]any{"daily_usd": 1.005}}},
		{"non member", map[string]any{"users": []any{map[string]any{"user_id": "00000000-0000-0000-0000-000000000001", "daily_usd": 1}}}},
		{"duplicate user", map[string]any{"users": []any{
			map[string]any{"user_id": testUserID, "daily_usd": 1},
			map[string]any{"user_id": testUserID, "weekly_usd": 1},
		}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			testutil.Call(t, testHandler.PutRuntimeCostBudget, budgetRequest(t, testUserID, http.MethodPut, runtimeID, tc.body)).Want(http.StatusBadRequest)
		})
	}
}

func TestPutRuntimeCostBudgetReplacesAndDropsEmptiedRows(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	runtimeID := dbfx.Runtime(t, "budget-rt-replace", testutil.Cols{"visibility": "public"})
	t.Cleanup(func() { dbfx.Exec(t, `DELETE FROM runtime_cost_budget WHERE runtime_id = $1`, runtimeID) })
	first := map[string]any{
		"runtime": map[string]any{"daily_usd": 20, "weekly_usd": 300},
		"users":   []any{map[string]any{"user_id": testUserID, "monthly_usd": 200}},
	}
	testutil.Call(t, testHandler.PutRuntimeCostBudget, budgetRequest(t, testUserID, http.MethodPut, runtimeID, first)).Want(http.StatusOK)
	if n := dbfx.Count(t, `SELECT count(*) FROM runtime_cost_budget WHERE runtime_id = $1`, runtimeID); n != 2 {
		t.Fatalf("rows after first put = %d, want 2", n)
	}
	// Runtime total emptied and the user row dropped from the list: both go away.
	second := map[string]any{"runtime": map[string]any{}, "users": []any{}}
	var out map[string]any
	testutil.Call(t, testHandler.PutRuntimeCostBudget, budgetRequest(t, testUserID, http.MethodPut, runtimeID, second)).Want(http.StatusOK).JSON(&out)
	if n := dbfx.Count(t, `SELECT count(*) FROM runtime_cost_budget WHERE runtime_id = $1`, runtimeID); n != 0 {
		t.Fatalf("rows after empty put = %d, want 0", n)
	}
	if out["runtime"] != nil || len(out["users"].([]any)) != 0 {
		t.Fatalf("empty state = %#v", out)
	}
}

func TestGetRuntimeCostBudgetReportsUsedAndReached(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	runtimeID := dbfx.Runtime(t, "budget-rt-get", testutil.Cols{"visibility": "public"})
	agentID := dbfx.Agent(t, "budget-agent", runtimeID, testutil.Cols{"owner_id": testUserID})
	taskID := dbfx.Task(t, agentID, testutil.Cols{"runtime_id": runtimeID, "status": "completed"})
	dbfx.Insert(t, "task_usage", testutil.Cols{
		"task_id": taskID, "provider": "xai", "model": "grok-4.5",
		"input_tokens": int64(0), "output_tokens": int64(0), "cache_read_tokens": int64(0), "cache_write_tokens": int64(0),
		"cost_usd_ticks": int64(250_000_000_000), // $25
	})
	t.Cleanup(func() { dbfx.Exec(t, `DELETE FROM runtime_cost_budget WHERE runtime_id = $1`, runtimeID) })
	body := map[string]any{
		"runtime": map[string]any{"monthly_usd": 100},
		"users":   []any{map[string]any{"user_id": testUserID, "daily_usd": 20}},
	}
	testutil.Call(t, testHandler.PutRuntimeCostBudget, budgetRequest(t, testUserID, http.MethodPut, runtimeID, body)).Want(http.StatusOK)

	var out map[string]any
	testutil.Call(t, testHandler.GetRuntimeCostBudget, budgetRequest(t, testUserID, http.MethodGet, runtimeID, nil)).Want(http.StatusOK).JSON(&out)
	monthly := out["runtime"].(map[string]any)["monthly"].(map[string]any)
	if monthly["used_usd"].(float64) != 25 || monthly["reached"] != false {
		t.Fatalf("runtime.monthly = %#v", monthly)
	}
	users := out["users"].([]any)
	daily := users[0].(map[string]any)["daily"].(map[string]any)
	if daily["used_usd"].(float64) != 25 || daily["reached"] != true {
		t.Fatalf("users[0].daily = %#v", daily)
	}
	if daily["reset_at"] == "" || daily["period_start"] == "" {
		t.Fatalf("period timestamps missing: %#v", daily)
	}
}

// can_manage mirrors the PUT gate, so it is true only for the runtime owner —
// a workspace admin reading the same card gets false and no Edit button.
func TestGetRuntimeCostBudgetCanManageIsRuntimeOwnerOnly(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ownerUserID := dbfx.User(t, "Budget Card Owner", "budget-card-owner@example.com")
	dbfx.Member(t, testWorkspaceID, ownerUserID, "member")
	runtimeID := dbfx.Runtime(t, "budget-rt-member", testutil.Cols{"visibility": "public", "owner_id": ownerUserID})
	memberUserID := dbfx.User(t, "Budget Viewer", "budget-viewer@example.com")
	dbfx.Member(t, testWorkspaceID, memberUserID, "member")
	adminUserID := dbfx.User(t, "Budget Card Admin", "budget-card-admin@example.com")
	dbfx.Member(t, testWorkspaceID, adminUserID, "admin")

	for _, tc := range []struct {
		name   string
		userID string
		want   bool
	}{
		{"runtime owner", ownerUserID, true},
		{"plain member", memberUserID, false},
		{"workspace admin", adminUserID, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out map[string]any
			testutil.Call(t, testHandler.GetRuntimeCostBudget, budgetRequest(t, tc.userID, http.MethodGet, runtimeID, nil)).Want(http.StatusOK).JSON(&out)
			if out["can_manage"] != tc.want {
				t.Fatalf("can_manage = %#v, want %v", out["can_manage"], tc.want)
			}
		})
	}
}

// A profile delete removes its runtime rows in bulk through
// DeleteAgentRuntimesByProfile rather than one DeleteAgentRuntime per runtime,
// so it needs its own budget cleanup: runtime_cost_budget carries no foreign
// key, and rows keyed by a deleted runtime_id are unreachable afterwards.
func TestDeleteRuntimeProfileRemovesRuntimeBudgets(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	profileID := insertRuntimeProfileFixture(t, ctx, "Budget Profile", "codex", "budget-profile-codex")
	runtimeID := insertProfileRuntimeFixture(t, ctx, profileID, "Budget Profile Runtime", "codex")
	t.Cleanup(func() { dbfx.Exec(t, `DELETE FROM runtime_cost_budget WHERE runtime_id = $1`, runtimeID) })

	body := map[string]any{"runtime": map[string]any{"daily_usd": 3}}
	testutil.Call(t, testHandler.PutRuntimeCostBudget, budgetRequest(t, testUserID, http.MethodPut, runtimeID, body)).Want(http.StatusOK)

	req := withURLParams(
		newRequest(http.MethodDelete, "/api/workspaces/"+testWorkspaceID+"/runtime-profiles/"+profileID, nil),
		"id", testWorkspaceID, "profileId", profileID,
	)
	testutil.Call(t, testHandler.DeleteRuntimeProfile, req).Want(http.StatusNoContent)

	if n := dbfx.Count(t, `SELECT count(*) FROM runtime_cost_budget WHERE runtime_id = $1`, runtimeID); n != 0 {
		t.Fatalf("budget rows survived runtime profile delete: %d", n)
	}
}

func TestDeleteRuntimeRemovesItsBudgets(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	runtimeID := dbfx.Runtime(t, "budget-rt-delete", testutil.Cols{"visibility": "public", "owner_id": testUserID})
	body := map[string]any{"runtime": map[string]any{"daily_usd": 1}}
	testutil.Call(t, testHandler.PutRuntimeCostBudget, budgetRequest(t, testUserID, http.MethodPut, runtimeID, body)).Want(http.StatusOK)
	req := withURLParam(newRequest(http.MethodDelete, "/api/runtimes/"+runtimeID, nil), "runtimeId", runtimeID)
	testutil.Call(t, testHandler.DeleteAgentRuntime, req).WantOneOf(http.StatusOK, http.StatusNoContent)
	if n := dbfx.Count(t, `SELECT count(*) FROM runtime_cost_budget WHERE runtime_id = $1`, runtimeID); n != 0 {
		t.Fatalf("budget rows survived runtime delete: %d", n)
	}
}
