package realtime

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/auth"
)

// fakeAccountChecker lets tests control the outcome of AccountChecker.Check
// without touching the database.
type fakeAccountChecker struct {
	err error
}

func (f fakeAccountChecker) Check(_ context.Context, _ string) error {
	return f.err
}

func TestAuthenticateTokenRejectsSuspended(t *testing.T) {
	token := makeTestToken(t)

	uid, errMsg := authenticateToken(token, nil, fakeAccountChecker{err: auth.ErrAccountSuspended}, context.Background())

	if uid != "" {
		t.Fatalf("uid = %q, want empty", uid)
	}
	want := `{"error":"account suspended","code":"ACCOUNT_SUSPENDED"}`
	if errMsg != want {
		t.Fatalf("errMsg = %q, want %q", errMsg, want)
	}
}

// TestAuthenticateTokenTransientErrorDoesNotClaimSuspension guards against
// conflating a transient AccountChecker failure (e.g. a DB error) with a
// confirmed suspension. The ws-client only force-logs-out a session on the
// ACCOUNT_SUSPENDED code, so a transient failure must not carry that code —
// otherwise a healthy user gets falsely told their account is suspended.
func TestAuthenticateTokenTransientErrorDoesNotClaimSuspension(t *testing.T) {
	token := makeTestToken(t)

	uid, errMsg := authenticateToken(token, nil, fakeAccountChecker{err: errors.New("db unavailable")}, context.Background())

	if uid != "" {
		t.Fatalf("uid = %q, want empty", uid)
	}
	if strings.Contains(errMsg, "ACCOUNT_SUSPENDED") {
		t.Fatalf("errMsg = %q, must not contain ACCOUNT_SUSPENDED for a transient error", errMsg)
	}
	if errMsg != accountStatusUnavailableErrMsg {
		t.Fatalf("errMsg = %q, want %q", errMsg, accountStatusUnavailableErrMsg)
	}
}

func TestAuthenticateTokenAllowsWhenCheckerNil(t *testing.T) {
	token := makeTestToken(t)

	uid, errMsg := authenticateToken(token, nil, nil, context.Background())

	if errMsg != "" {
		t.Fatalf("errMsg = %q, want empty", errMsg)
	}
	if uid != testUserID {
		t.Fatalf("uid = %q, want %q", uid, testUserID)
	}
}

func TestAuthenticateTokenAllowsWhenCheckerPasses(t *testing.T) {
	token := makeTestToken(t)

	uid, errMsg := authenticateToken(token, nil, fakeAccountChecker{err: nil}, context.Background())

	if errMsg != "" {
		t.Fatalf("errMsg = %q, want empty", errMsg)
	}
	if uid != testUserID {
		t.Fatalf("uid = %q, want %q", uid, testUserID)
	}
}

func TestDisconnectUserEvictsAllUserConnections(t *testing.T) {
	hub := NewHub()

	newClient := func(userID, workspaceID string) *Client {
		return &Client{
			hub:           hub,
			send:          make(chan []byte, 4),
			userID:        userID,
			workspaceID:   workspaceID,
			subscriptions: make(map[scopeKey]bool),
		}
	}

	a1 := newClient("user-a", "ws-1")
	a2 := newClient("user-a", "ws-2")
	b1 := newClient("user-b", "ws-1")

	hub.mu.Lock()
	hub.clients[a1] = true
	hub.clients[a2] = true
	hub.clients[b1] = true
	hub.mu.Unlock()

	hub.DisconnectUser("user-a")

	// A's clients must be removed from the hub and their send channels
	// closed (evictSlow's teardown), after receiving the auth_error frame.
	for _, c := range []*Client{a1, a2} {
		select {
		case payload, ok := <-c.send:
			if !ok {
				t.Fatalf("client send channel closed before delivering auth_error frame")
			}
			want := `{"type":"auth_error","payload":{"error":"account suspended","code":"ACCOUNT_SUSPENDED"}}`
			if string(payload) != want {
				t.Fatalf("payload = %q, want %q", payload, want)
			}
		default:
			t.Fatal("expected auth_error frame on send channel")
		}

		// Channel should now be closed by evictSlow.
		if _, ok := <-c.send; ok {
			t.Fatal("expected send channel to be closed after eviction")
		}
	}

	hub.mu.RLock()
	if hub.clients[a1] || hub.clients[a2] {
		t.Fatal("user-a clients should have been removed from hub.clients")
	}
	if !hub.clients[b1] {
		t.Fatal("user-b client should be untouched")
	}
	hub.mu.RUnlock()

	// user-b's send channel must remain open and empty.
	select {
	case payload := <-b1.send:
		t.Fatalf("unexpected payload on user-b send channel: %s", payload)
	default:
	}
}

// The ws-client only recognizes frames carrying a string `type` — a raw
// {"error":...} rejection is silently dropped and the client reconnects
// forever. Genuine credential rejections must therefore be wrapped in the
// typed auth_error envelope, while transient conditions (status lookup
// unavailable, first-frame read timeout) must stay unwrapped so a typed
// auth_error never permanently stops a healthy client's reconnect loop.
func TestWSAuthClosePayloadWrapsRejectionsOnly(t *testing.T) {
	got := string(wsAuthClosePayload(accountSuspendedErrMsg))
	want := `{"type":"auth_error","payload":{"error":"account suspended","code":"ACCOUNT_SUSPENDED"}}`
	if got != want {
		t.Fatalf("suspended payload = %q, want %q", got, want)
	}

	invalid := `{"error":"invalid token"}`
	if got := string(wsAuthClosePayload(invalid)); got != `{"type":"auth_error","payload":{"error":"invalid token"}}` {
		t.Fatalf("invalid-token payload not wrapped: %q", got)
	}

	if got := string(wsAuthClosePayload(accountStatusUnavailableErrMsg)); got != accountStatusUnavailableErrMsg {
		t.Fatalf("transient status payload must stay raw, got %q", got)
	}
	if got := string(wsAuthClosePayload(wsAuthTimeoutErrMsg)); got != wsAuthTimeoutErrMsg {
		t.Fatalf("timeout payload must stay raw, got %q", got)
	}
}
