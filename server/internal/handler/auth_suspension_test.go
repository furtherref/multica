package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/multica-ai/multica/server/internal/auth"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// TestVerifyCodeRejectsSuspendedUser covers the existing-user login path:
// a suspended account must be rejected with 403 + ACCOUNT_SUSPENDED even
// though the verification code itself is valid.
func TestVerifyCodeRejectsSuspendedUser(t *testing.T) {
	const email = "suspended-verify-test@multica.ai"
	ctx := context.Background()

	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM verification_code WHERE email = $1`, email)
		testPool.Exec(ctx, `DELETE FROM "user" WHERE email = $1`, email)
	})

	// Create the user up front (so this exercises the existing-user branch,
	// not signup), then suspend it.
	user, err := testHandler.Queries.CreateUser(ctx, db.CreateUserParams{
		Name:  "Suspended Verify User",
		Email: email,
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if _, err := testHandler.Queries.SetUserAccountStatus(ctx, db.SetUserAccountStatusParams{
		ID:            user.ID,
		AccountStatus: auth.AccountStatusSuspended,
	}); err != nil {
		t.Fatalf("SetUserAccountStatus: %v", err)
	}

	// Insert the verification code directly (rather than going through
	// SendCode, which now also rejects a suspended account before a code is
	// ever issued) so this test exercises VerifyCode's own suspension check
	// in isolation.
	const code = "424242"
	createVerificationCodeForTest(t, email, code)

	// Verify with the correct code — the account is suspended, so this must
	// still be rejected.
	w := httptest.NewRecorder()
	var buf bytes.Buffer
	json.NewEncoder(&buf).Encode(map[string]string{"email": email, "code": code})
	req := httptest.NewRequest("POST", "/auth/verify-code", &buf)
	req.Header.Set("Content-Type", "application/json")
	testHandler.VerifyCode(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("VerifyCode: expected 403, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["code"] != auth.AccountSuspendedCode {
		t.Fatalf("VerifyCode: expected code %q, got %q (body: %s)", auth.AccountSuspendedCode, resp["code"], w.Body.String())
	}
}

// TestIssueCliTokenRejectsSuspendedUser covers the token-mint path used by
// cookie-authenticated browser sessions handing a bearer token to the CLI.
func TestIssueCliTokenRejectsSuspendedUser(t *testing.T) {
	const email = "suspended-cli-test@multica.ai"
	ctx := context.Background()

	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM "user" WHERE email = $1`, email)
	})

	user, err := testHandler.Queries.CreateUser(ctx, db.CreateUserParams{
		Name:  "Suspended CLI User",
		Email: email,
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if _, err := testHandler.Queries.SetUserAccountStatus(ctx, db.SetUserAccountStatusParams{
		ID:            user.ID,
		AccountStatus: auth.AccountStatusSuspended,
	}); err != nil {
		t.Fatalf("SetUserAccountStatus: %v", err)
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/auth/cli-token", nil)
	req.Header.Set("X-User-ID", uuidToString(user.ID))
	testHandler.IssueCliToken(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("IssueCliToken: expected 403, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["code"] != auth.AccountSuspendedCode {
		t.Fatalf("IssueCliToken: expected code %q, got %q (body: %s)", auth.AccountSuspendedCode, resp["code"], w.Body.String())
	}
}
