package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

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
