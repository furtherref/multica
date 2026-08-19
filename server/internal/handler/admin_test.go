package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// patchAccountStatus builds a PATCH /api/admin/users/{id}/status request with
// {"status": status} as the body and the given actor's X-User-ID header, and
// wires the chi URL param the handler reads via chi.URLParam(r, "id").
func patchAccountStatus(t *testing.T, actorID, targetID, status string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]string{"status": status})
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req := httptest.NewRequest("PATCH", "/api/admin/users/"+targetID+"/status", bytes.NewReader(body))
	req.Header.Set("X-User-ID", actorID)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", targetID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()
	testHandler.SetUserAccountStatus(w, req)
	return w
}

func TestSetAccountStatusSuspendsAndRestores(t *testing.T) {
	const adminEmail = "admin-set-status-test@multica.ai"
	const targetEmail = "target-set-status-test@multica.ai"
	ctx := context.Background()

	// Uses a dedicated target user, never the shared testUserID fixture:
	// suspending testUserID would run quiesceSuspendedUser against the
	// package-wide shared "Handler Test Runtime" it owns (see
	// setupHandlerTestFixture), force-offlining it and cancelling any
	// queued tasks other tests in this package depend on.
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM "user" WHERE email IN ($1, $2)`, adminEmail, targetEmail)
	})

	adminUser, err := testHandler.Queries.CreateUser(ctx, db.CreateUserParams{
		Name:  "Admin Set Status Test",
		Email: adminEmail,
	})
	if err != nil {
		t.Fatalf("CreateUser admin: %v", err)
	}
	targetUser, err := testHandler.Queries.CreateUser(ctx, db.CreateUserParams{
		Name:  "Target Set Status Test",
		Email: targetEmail,
	})
	if err != nil {
		t.Fatalf("CreateUser target: %v", err)
	}
	targetIDStr := uuidToString(targetUser.ID)
	withAdminEmails(t, []string{adminEmail})

	w := patchAccountStatus(t, uuidToString(adminUser.ID), targetIDStr, "suspended")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 suspending, got %d: %s", w.Code, w.Body.String())
	}
	var resp AdminUserResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.AccountStatus != "suspended" {
		t.Fatalf("expected account_status=suspended in response, got %q", resp.AccountStatus)
	}
	var status string
	if err := testPool.QueryRow(ctx, `SELECT account_status FROM "user" WHERE id = $1`, targetIDStr).Scan(&status); err != nil {
		t.Fatalf("read account_status: %v", err)
	}
	if status != "suspended" {
		t.Fatalf("DB account_status = %q, want suspended", status)
	}

	w = patchAccountStatus(t, uuidToString(adminUser.ID), targetIDStr, "active")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 restoring, got %d: %s", w.Code, w.Body.String())
	}
	if err := testPool.QueryRow(ctx, `SELECT account_status FROM "user" WHERE id = $1`, targetIDStr).Scan(&status); err != nil {
		t.Fatalf("read account_status: %v", err)
	}
	if status != "active" {
		t.Fatalf("DB account_status = %q, want active", status)
	}
}

func TestSetAccountStatusRejectsSelf(t *testing.T) {
	const adminEmail = "admin-set-status-self-test@multica.ai"
	ctx := context.Background()

	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM "user" WHERE email = $1`, adminEmail)
	})

	adminUser, err := testHandler.Queries.CreateUser(ctx, db.CreateUserParams{
		Name:  "Admin Set Status Self Test",
		Email: adminEmail,
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	withAdminEmails(t, []string{adminEmail})

	adminIDStr := uuidToString(adminUser.ID)
	w := patchAccountStatus(t, adminIDStr, adminIDStr, "suspended")
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 self-status-change, got %d: %s", w.Code, w.Body.String())
	}
	var status string
	if err := testPool.QueryRow(ctx, `SELECT account_status FROM "user" WHERE id = $1`, adminIDStr).Scan(&status); err != nil {
		t.Fatalf("read account_status: %v", err)
	}
	if status != "active" {
		t.Fatalf("self-change must not mutate status; got %q", status)
	}
}

func TestSetAccountStatusRejectsUnknownStatus(t *testing.T) {
	const adminEmail = "admin-set-status-unknown-test@multica.ai"
	ctx := context.Background()

	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM "user" WHERE email = $1`, adminEmail)
	})

	adminUser, err := testHandler.Queries.CreateUser(ctx, db.CreateUserParams{
		Name:  "Admin Set Status Unknown Test",
		Email: adminEmail,
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	withAdminEmails(t, []string{adminEmail})

	w := patchAccountStatus(t, uuidToString(adminUser.ID), testUserID, "banned")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for unknown status, got %d: %s", w.Code, w.Body.String())
	}
}

func TestSetAccountStatusUnknownUser404(t *testing.T) {
	const adminEmail = "admin-set-status-unknown-user-test@multica.ai"
	ctx := context.Background()

	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM "user" WHERE email = $1`, adminEmail)
	})

	adminUser, err := testHandler.Queries.CreateUser(ctx, db.CreateUserParams{
		Name:  "Admin Set Status Unknown User Test",
		Email: adminEmail,
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	withAdminEmails(t, []string{adminEmail})

	// A well-formed UUID that matches no row in "user".
	missingID := uuid.NewString()
	w := patchAccountStatus(t, uuidToString(adminUser.ID), missingID, "suspended")
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown user, got %d: %s", w.Code, w.Body.String())
	}
}

func TestSuspendCancelsRuntimesAndTasks(t *testing.T) {
	const adminEmail = "admin-set-status-suspend-cancel-test@multica.ai"
	const targetEmail = "target-set-status-suspend-cancel-test@multica.ai"
	ctx := context.Background()

	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM "user" WHERE email IN ($1, $2)`, adminEmail, targetEmail)
	})

	adminUser, err := testHandler.Queries.CreateUser(ctx, db.CreateUserParams{
		Name:  "Admin Suspend Cancel Test",
		Email: adminEmail,
	})
	if err != nil {
		t.Fatalf("CreateUser admin: %v", err)
	}
	targetUser, err := testHandler.Queries.CreateUser(ctx, db.CreateUserParams{
		Name:  "Target Suspend Cancel Test",
		Email: targetEmail,
	})
	if err != nil {
		t.Fatalf("CreateUser target: %v", err)
	}
	targetIDStr := uuidToString(targetUser.ID)
	if _, err := testPool.Exec(ctx,
		`INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'member')`,
		testWorkspaceID, targetIDStr,
	); err != nil {
		t.Fatalf("add target member: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM member WHERE workspace_id = $1 AND user_id = $2`, testWorkspaceID, targetIDStr)
	})
	withAdminEmails(t, []string{adminEmail})

	var runtimeID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (workspace_id, daemon_id, name, runtime_mode, provider, status, device_info, metadata, last_seen_at, visibility, owner_id)
		VALUES ($1, NULL, 'Suspend Cancel Runtime', 'cloud', 'handler_test_runtime', 'online', 'x', '{}'::jsonb, now(), 'private', $2)
		RETURNING id`, testWorkspaceID, targetIDStr).Scan(&runtimeID); err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, runtimeID)
	})

	agentID, issueID := createClaimReclaimAgentAndIssue(t, ctx, runtimeID, "Suspend Cancel Agent")
	taskID := seedQueuedIssueTask(t, ctx, agentID, runtimeID, issueID)

	w := patchAccountStatus(t, uuidToString(adminUser.ID), targetIDStr, "suspended")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 suspending, got %d: %s", w.Code, w.Body.String())
	}

	var taskStatus string
	if err := testPool.QueryRow(ctx, `SELECT status FROM agent_task_queue WHERE id = $1`, taskID).Scan(&taskStatus); err != nil {
		t.Fatalf("read task status: %v", err)
	}
	if taskStatus != "cancelled" {
		t.Fatalf("task status = %q, want cancelled", taskStatus)
	}

	var runtimeStatus string
	if err := testPool.QueryRow(ctx, `SELECT status FROM agent_runtime WHERE id = $1`, runtimeID).Scan(&runtimeStatus); err != nil {
		t.Fatalf("read runtime status: %v", err)
	}
	if runtimeStatus != "offline" {
		t.Fatalf("runtime status = %q, want offline", runtimeStatus)
	}
}

// withAdminEmails temporarily swaps testHandler's admin allowlist and
// restores it after the test, since testHandler is a package-level
// singleton shared across the suite.
func withAdminEmails(t *testing.T, emails []string) {
	t.Helper()
	prev := testHandler.cfg.AdminEmails
	testHandler.cfg.AdminEmails = emails
	t.Cleanup(func() {
		testHandler.cfg.AdminEmails = prev
	})
}

func TestListAllUsersRequiresSystemAdmin(t *testing.T) {
	const adminEmail = "admin-list-users-test@multica.ai"
	ctx := context.Background()

	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM "user" WHERE email = $1`, adminEmail)
	})

	adminUser, err := testHandler.Queries.CreateUser(ctx, db.CreateUserParams{
		Name:  "Admin List Users Test",
		Email: adminEmail,
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	t.Run("non_admin_forbidden", func(t *testing.T) {
		withAdminEmails(t, []string{adminEmail})

		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/api/admin/users", nil)
		req.Header.Set("X-User-ID", testUserID) // fixture user, not in AdminEmails
		testHandler.ListAllUsers(w, req)

		if w.Code != http.StatusForbidden {
			t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("admin_ok", func(t *testing.T) {
		withAdminEmails(t, []string{adminEmail})

		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/api/admin/users", nil)
		req.Header.Set("X-User-ID", uuidToString(adminUser.ID))
		testHandler.ListAllUsers(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}

		var resp struct {
			Users []AdminUserResponse `json:"users"`
		}
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if len(resp.Users) == 0 {
			t.Fatal("expected at least one user in the response")
		}
		found := false
		for _, u := range resp.Users {
			if u.ID == uuidToString(adminUser.ID) {
				found = true
				if u.AccountStatus == "" {
					t.Fatal("expected account_status to be populated")
				}
			}
		}
		if !found {
			t.Fatal("expected admin user to appear in the users list")
		}
	})

	// Admin-email membership is case-insensitive.
	t.Run("admin_email_case_insensitive", func(t *testing.T) {
		withAdminEmails(t, []string{"ADMIN-LIST-USERS-TEST@MULTICA.AI"})

		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/api/admin/users", nil)
		req.Header.Set("X-User-ID", uuidToString(adminUser.ID))
		testHandler.ListAllUsers(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}
	})

	// A caller cannot bypass the allowlist by spoofing X-User-Email; identity
	// must come from the user row resolved via X-User-ID.
	t.Run("spoofed_email_header_ignored", func(t *testing.T) {
		withAdminEmails(t, []string{adminEmail})

		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/api/admin/users", nil)
		req.Header.Set("X-User-ID", testUserID)
		req.Header.Set("X-User-Email", adminEmail)
		testHandler.ListAllUsers(w, req)

		if w.Code != http.StatusForbidden {
			t.Fatalf("expected 403 despite spoofed X-User-Email, got %d: %s", w.Code, w.Body.String())
		}
	})
}

func TestGetMeReportsSystemAdmin(t *testing.T) {
	const adminEmail = "admin-getme-test@multica.ai"
	ctx := context.Background()

	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM "user" WHERE email = $1`, adminEmail)
	})

	adminUser, err := testHandler.Queries.CreateUser(ctx, db.CreateUserParams{
		Name:  "Admin GetMe Test",
		Email: adminEmail,
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	t.Run("admin_true", func(t *testing.T) {
		withAdminEmails(t, []string{adminEmail})

		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/api/me", nil)
		req.Header.Set("X-User-ID", uuidToString(adminUser.ID))
		testHandler.GetMe(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}
		var resp UserResponse
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if !resp.IsSystemAdmin {
			t.Fatal("expected is_system_admin=true for admin user")
		}
	})

	t.Run("non_admin_false_or_absent", func(t *testing.T) {
		withAdminEmails(t, []string{adminEmail})

		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/api/me", nil)
		req.Header.Set("X-User-ID", testUserID)
		testHandler.GetMe(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}

		var raw map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
			t.Fatalf("decode raw response: %v", err)
		}
		if v, ok := raw["is_system_admin"]; ok && v != false {
			t.Fatalf("expected is_system_admin absent or false for non-admin, got %v", v)
		}

		var resp UserResponse
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if resp.IsSystemAdmin {
			t.Fatal("expected is_system_admin=false for non-admin user")
		}
	})
}
