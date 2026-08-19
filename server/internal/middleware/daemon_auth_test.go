package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/auth"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// TestDaemonAuth_PATCacheHit pins the PAT-fallback short-circuit. Production
// daemon traffic today uses mul_ PATs (mdt_ minting isn't wired up yet), so
// this is the cache hit that actually matters for /api/daemon/* DB load.
func TestDaemonAuth_PATCacheHit(t *testing.T) {
	rdb := newRedisTestClient(t)
	cache := auth.NewPATCache(rdb)
	if cache == nil {
		t.Fatal("expected non-nil cache")
	}

	const rawToken = "mul_daemon_pat_cache_hit_test"
	hash := auth.HashToken(rawToken)
	cache.Set(context.Background(), hash, "cached-user-id", auth.AuthCacheTTL)

	var gotUserID, gotPath string
	mw := DaemonAuth(nil, cache, nil, nil)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUserID = r.Header.Get("X-User-ID")
		gotPath = DaemonAuthPathFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("POST", "/api/daemon/heartbeat", nil)
	req.Header.Set("Authorization", "Bearer "+rawToken)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if gotUserID != "cached-user-id" {
		t.Fatalf("expected cached X-User-ID, got %q", gotUserID)
	}
	if gotPath != DaemonAuthPathPAT {
		t.Fatalf("expected auth path %q, got %q", DaemonAuthPathPAT, gotPath)
	}
}

func TestDaemonAuth_MissingAuth(t *testing.T) {
	mw := DaemonAuth(nil, nil, nil, nil)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next must not be called")
	}))
	req := httptest.NewRequest("POST", "/api/daemon/heartbeat", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

// TestDaemonAuth_StripsClientSuppliedActorSource mirrors the
// TestAuth_StripsClientSuppliedActorSource invariant for the daemon
// auth path: a client supplying X-Actor-Source must NOT leak that
// header through to the handler. Required for parity between the
// two middlewares — the regular Auth path strips at the top, and we
// added the same strip in DaemonAuth so account-level guards (e.g.
// handler.RequireHumanActor) can trust the header regardless of
// which auth chain a request arrived on.
//
// We exercise an mdt_ token with an attempted forged X-Actor-Source.
// On the mdt_ path no actor-source stamp is added (daemon tokens
// aren't a "machine credential" in the billing sense — they're a
// runtime-bound proof for the daemon API itself), so a clean strip
// leaves the header empty downstream.
func TestDaemonAuth_StripsClientSuppliedActorSource(t *testing.T) {
	pool := openPool(t)
	defer pool.Close()
	queries := db.New(pool)

	const rawToken = "mdt_strip_test"
	hash := auth.HashToken(rawToken)
	wsUUID := createDaemonAuthTestWorkspace(t, pool, "mdt-strip-test-ws")
	if _, err := queries.CreateDaemonToken(context.Background(), db.CreateDaemonTokenParams{
		TokenHash:   hash,
		WorkspaceID: wsUUID,
		DaemonID:    "daemon-strip-test",
		ExpiresAt:   pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
	}); err != nil {
		t.Fatalf("CreateDaemonToken: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM daemon_token WHERE token_hash = $1`, hash)
	})

	var gotActorSource string
	mw := DaemonAuth(queries, nil, nil, nil)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotActorSource = r.Header.Get("X-Actor-Source")
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("POST", "/api/daemon/heartbeat", nil)
	req.Header.Set("Authorization", "Bearer "+rawToken)
	// Forged value the client tries to smuggle in.
	req.Header.Set("X-Actor-Source", "cloud_pat")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if gotActorSource != "" {
		t.Fatalf("X-Actor-Source must be cleared on the mdt_ path, got %q", gotActorSource)
	}
}

func TestDaemonAuth_InvalidMDT_NilQueries(t *testing.T) {
	mw := DaemonAuth(nil, nil, nil, nil) // no caches, no DB
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next must not be called")
	}))
	req := httptest.NewRequest("POST", "/api/daemon/heartbeat", nil)
	req.Header.Set("Authorization", "Bearer mdt_unknown")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

// TestDaemonAuth_MCN_NoVerifierConfigured pins the fail-closed
// behaviour when MULTICA_CLOUD_FLEET_URL is empty: an mcn_ token MUST
// be rejected at the prefix branch with 401, not silently fall
// through to the mul_/JWT paths (an mcn_ string would never match a
// valid PAT or JWT, but failing closed makes the contract explicit).
func TestDaemonAuth_MCN_NoVerifierConfigured(t *testing.T) {
	mw := DaemonAuth(nil, nil, nil, nil)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next must not be called when verifier is unconfigured")
	}))
	req := httptest.NewRequest("POST", "/api/daemon/heartbeat", nil)
	req.Header.Set("Authorization", "Bearer mcn_anything")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 with no verifier, got %d", w.Code)
	}
}

// TestDaemonAuth_MCN_ValidTokenSetsUserID confirms that on a successful
// Fleet verify, DaemonAuth surfaces owner_id as X-User-ID and tags the
// auth path as cloud_pat for telemetry. We use a stub Fleet here
// (no Redis) so the test runs without external services.
func TestDaemonAuth_MCN_ValidTokenSetsUserID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"valid": true,
			"owner_id": "01972f7e-7e8d-77ef-a13d-1b0ce3e9c001",
			"instance_id": "i-01"
		}`))
	}))
	defer srv.Close()

	verifier := auth.NewCloudPATVerifier(auth.CloudPATVerifierConfig{FleetBaseURL: srv.URL})

	var gotUser, gotPath, gotActorSource string
	mw := DaemonAuth(nil, nil, verifier, nil)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser = r.Header.Get("X-User-ID")
		gotPath = DaemonAuthPathFromContext(r.Context())
		gotActorSource = r.Header.Get("X-Actor-Source")
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("POST", "/api/daemon/heartbeat", nil)
	req.Header.Set("Authorization", "Bearer mcn_some_token")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if gotUser != "01972f7e-7e8d-77ef-a13d-1b0ce3e9c001" {
		t.Errorf("expected owner_id propagated as X-User-ID, got %q", gotUser)
	}
	if gotPath != DaemonAuthPathCloudPAT {
		t.Errorf("expected auth path %q, got %q", DaemonAuthPathCloudPAT, gotPath)
	}
	// Mirror the regular Auth middleware's stamp. Daemon routes don't
	// currently sit behind RequireHumanActor, but we want the two
	// auth paths to behave identically on this header so an endpoint
	// that ever moves between them, or shares both, can't be tricked
	// into thinking an mcn_ caller is human.
	if gotActorSource != "cloud_pat" {
		t.Errorf("expected X-Actor-Source=cloud_pat, got %q", gotActorSource)
	}
}

// TestDaemonAuth_MCN_FleetSaysInvalid confirms that a valid:false
// Fleet response maps to 401 (not 503) — the token IS known to be bad,
// retrying won't help.
func TestDaemonAuth_MCN_FleetSaysInvalid(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"valid":false,"reason":"token_revoked"}`))
	}))
	defer srv.Close()

	verifier := auth.NewCloudPATVerifier(auth.CloudPATVerifierConfig{FleetBaseURL: srv.URL})
	mw := DaemonAuth(nil, nil, verifier, nil)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next must not be called when fleet says invalid")
	}))

	req := httptest.NewRequest("POST", "/api/daemon/heartbeat", nil)
	req.Header.Set("Authorization", "Bearer mcn_revoked")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for invalid token, got %d", w.Code)
	}
}

// TestDaemonAuth_MCN_FleetUnreachable confirms the Unavailable branch
// emits 503 — the daemon must distinguish "your token is bad" (401, drop
// it) from "cloud is down" (503, retry later) so a brief outage doesn't
// invalidate everyone's PAT.
func TestDaemonAuth_MCN_FleetUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	verifier := auth.NewCloudPATVerifier(auth.CloudPATVerifierConfig{FleetBaseURL: srv.URL})
	mw := DaemonAuth(nil, nil, verifier, nil)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next must not be called when fleet is unavailable")
	}))

	req := httptest.NewRequest("POST", "/api/daemon/heartbeat", nil)
	req.Header.Set("Authorization", "Bearer mcn_x")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when fleet is unavailable, got %d", w.Code)
	}
}


// TestDaemonAuth_MCN_OwnerNotInLocalDB pins the new owner-existence
// guard end-to-end through the middleware. Cloud verifies the token
// successfully and returns an owner_id that does not exist in our
// local user table — DaemonAuth must reject with 401 (not 503) and
// MUST NOT call the next handler with a phantom X-User-ID.
func TestDaemonAuth_MCN_OwnerNotInLocalDB(t *testing.T) {
	pool := openPool(t)
	defer pool.Close()
	queries := db.New(pool)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Owner_id is syntactically valid but not seeded in the DB.
		_, _ = w.Write([]byte(`{
			"valid": true,
			"owner_id": "00000000-0000-0000-0000-0000000feed1",
			"instance_id": "i-99"
		}`))
	}))
	defer srv.Close()

	verifier := auth.NewCloudPATVerifier(auth.CloudPATVerifierConfig{FleetBaseURL: srv.URL})

	mw := DaemonAuth(queries, nil, verifier, nil)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next must not be called when owner_id has no local user")
	}))

	req := httptest.NewRequest("POST", "/api/daemon/heartbeat", nil)
	req.Header.Set("Authorization", "Bearer mcn_phantom_owner")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 when local user is missing, got %d", w.Code)
	}
}

// TestDaemonAuth_MDTDeletedTokenRejectedImmediately pins the mdt_ path's
// per-request authority: there is deliberately NO positive cache in front of
// the daemon_token table (unlike mul_ PATs), so deleting the row — account
// suspension's revocation — takes effect on the very next request across the
// ENTIRE /api/daemon surface, not after a cache TTL.
func TestDaemonAuth_MDTDeletedTokenRejectedImmediately(t *testing.T) {
	pool := openPool(t)
	defer pool.Close()
	queries := db.New(pool)
	ctx := context.Background()

	const rawToken = "mdt_authoritative_revocation_test"
	hash := auth.HashToken(rawToken)
	wsUUID := createDaemonAuthTestWorkspace(t, pool, "mdt-revocation-test-ws")
	if _, err := queries.CreateDaemonToken(ctx, db.CreateDaemonTokenParams{
		TokenHash:   hash,
		WorkspaceID: wsUUID,
		DaemonID:    "daemon-authoritative-test",
		ExpiresAt:   pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
	}); err != nil {
		t.Fatalf("CreateDaemonToken: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM daemon_token WHERE token_hash = $1`, hash)
	})

	mw := DaemonAuth(queries, nil, nil, nil)
	okCalls := 0
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		okCalls++
		w.WriteHeader(http.StatusOK)
	}))

	makeReq := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/api/daemon/heartbeat", nil)
		req.Header.Set("Authorization", "Bearer "+rawToken)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w
	}

	if w := makeReq(); w.Code != http.StatusOK {
		t.Fatalf("expected 200 while token exists, got %d: %s", w.Code, w.Body.String())
	}
	if _, err := pool.Exec(ctx, `DELETE FROM daemon_token WHERE token_hash = $1`, hash); err != nil {
		t.Fatalf("delete token: %v", err)
	}
	if w := makeReq(); w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 immediately after deletion, got %d: %s", w.Code, w.Body.String())
	}
	if okCalls != 1 {
		t.Fatalf("next handler called %d times, want 1", okCalls)
	}
}


// createDaemonAuthTestWorkspace seeds a workspace row: daemon_token still
// carries a legacy FK to workspace, so mdt_ tests need a real parent row.
func createDaemonAuthTestWorkspace(t *testing.T, pool *pgxpool.Pool, slug string) pgtype.UUID {
	t.Helper()
	var wsID string
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO workspace (name, slug, description, issue_prefix) VALUES ($1, $1, '', 'MDT') RETURNING id`,
		slug,
	).Scan(&wsID); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, wsID)
	})
	var wsUUID pgtype.UUID
	if err := wsUUID.Scan(wsID); err != nil {
		t.Fatalf("scan ws uuid: %v", err)
	}
	return wsUUID
}
