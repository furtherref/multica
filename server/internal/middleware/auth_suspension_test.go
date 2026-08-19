package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/auth"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// seedSuspensionUser inserts a fresh user row (default account_status
// "active") and returns its UUID as the canonical hyphenated string.
// Mirrors seedOwnerLookupUser's lightweight fixture approach.
func seedSuspensionUser(t *testing.T, queries *db.Queries) string {
	t.Helper()
	ctx := context.Background()
	stamp := time.Now().UnixNano()
	user, err := queries.CreateUser(ctx, db.CreateUserParams{
		Name:  "suspension-test",
		Email: pgtypeUniqueEmail(stamp),
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return uuidToString(user.ID)
}

// setAccountStatus updates account_status directly via SQL — mirrors the
// raw-SQL fixture style used in workspace_test.go rather than depending on
// a sqlc mutation that may not exist yet.
func setAccountStatus(t *testing.T, pool *pgxpool.Pool, userID, status string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`UPDATE "user" SET account_status = $2 WHERE id = $1`, userID, status); err != nil {
		t.Fatalf("set account_status: %v", err)
	}
}

// TestAuthRejectsSuspendedJWT proves the JWT auth path enforces
// account_status: a suspended user's otherwise-valid JWT is rejected with
// 403 and the machine-readable ACCOUNT_SUSPENDED code.
func TestAuthRejectsSuspendedJWT(t *testing.T) {
	pool := openPool(t)
	defer pool.Close()
	queries := db.New(pool)

	userID := seedSuspensionUser(t, queries)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1`, userID)
	})
	setAccountStatus(t, pool, userID, auth.AccountStatusSuspended)

	guard := &auth.AccountGuard{Queries: queries}
	mw := Auth(nil, nil, nil, guard)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next handler should not be called for a suspended user")
	}))

	claims := validClaims()
	claims["sub"] = userID
	token := generateToken(claims, auth.JWTSecret())

	req := httptest.NewRequest("GET", "/api/me", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
	if body := w.Body.String(); !strings.Contains(body, "ACCOUNT_SUSPENDED") {
		t.Fatalf("expected body to contain ACCOUNT_SUSPENDED, got %s", body)
	}
}

// TestAuthRejectsSuspendedPATCacheHit proves suspension is enforced even on
// the PAT-cache-hit fast path — the regression this feature is guarding
// against (a suspended user's next request must fail even while the PAT
// cache is warm). The AccountGuard's own cache is nil so its status read
// hits the DB on every call, independent of the PAT cache being warm.
func TestAuthRejectsSuspendedPATCacheHit(t *testing.T) {
	pool := openPool(t)
	defer pool.Close()
	queries := db.New(pool)

	userID := seedSuspensionUser(t, queries)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1`, userID)
	})
	setAccountStatus(t, pool, userID, auth.AccountStatusSuspended)

	rdb := newRedisTestClient(t)
	patCache := auth.NewPATCache(rdb)

	const rawToken = "mul_suspended_cache_hit_test_token"
	hash := auth.HashToken(rawToken)
	patCache.Set(context.Background(), hash, userID, auth.AuthCacheTTL)

	guard := &auth.AccountGuard{Queries: queries} // nil Cache: every Check hits the DB
	mw := Auth(nil, patCache, nil, guard)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next handler should not be called for a suspended user")
	}))

	req := httptest.NewRequest("GET", "/api/me", nil)
	req.Header.Set("Authorization", "Bearer "+rawToken)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 on warm PAT cache hit, got %d: %s", w.Code, w.Body.String())
	}
	if body := w.Body.String(); !strings.Contains(body, "ACCOUNT_SUSPENDED") {
		t.Fatalf("expected body to contain ACCOUNT_SUSPENDED, got %s", body)
	}
}

// TestAuthAllowsActiveUser is the passthrough control: an active user's JWT
// must reach the next handler with X-User-ID set, unaffected by AccountGuard.
func TestAuthAllowsActiveUser(t *testing.T) {
	pool := openPool(t)
	defer pool.Close()
	queries := db.New(pool)

	userID := seedSuspensionUser(t, queries)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1`, userID)
	})
	// account_status defaults to "active" on insert — set explicitly so
	// this test documents the contract rather than relying on the default.
	setAccountStatus(t, pool, userID, auth.AccountStatusActive)

	guard := &auth.AccountGuard{Queries: queries}
	var gotUserID string
	mw := Auth(nil, nil, nil, guard)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUserID = r.Header.Get("X-User-ID")
		w.WriteHeader(http.StatusOK)
	}))

	claims := validClaims()
	claims["sub"] = userID
	token := generateToken(claims, auth.JWTSecret())

	req := httptest.NewRequest("GET", "/api/me", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if gotUserID != userID {
		t.Fatalf("expected X-User-ID %q, got %q", userID, gotUserID)
	}
}
