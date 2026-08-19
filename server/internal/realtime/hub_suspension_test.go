package realtime

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

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

// Cross-node suspension: the suspend path publishes the suspended control
// frame through the relay, and every node's delivery of a user-scoped frame
// lands in fanoutUser. Delivery alone would rely on the client honoring
// auth_error — the hub must instead evict server-side, so a non-cooperating
// client on a remote node loses its socket too.
func TestFanoutUserSuspendedFrameEvictsServerSide(t *testing.T) {
	hub := NewHub()
	c := &Client{hub: hub, userID: "user-x", workspaceID: "ws-1", send: make(chan []byte, 4)}
	hub.mu.Lock()
	hub.clients[c] = true
	hub.mu.Unlock()
	hub.subscribe(c, ScopeUser, "user-x")

	hub.SendToUser("user-x", AccountSuspendedFrame())

	// The client got the frame (cooperating clients still self-terminate)…
	select {
	case payload := <-c.send:
		if string(payload) != string(AccountSuspendedFrame()) {
			t.Fatalf("payload = %s", payload)
		}
	default:
		t.Fatal("expected suspended frame on send channel")
	}
	// …and was evicted server-side regardless. Guard with a timeout so an
	// unfixed hub (channel never closed) fails instead of hanging.
	select {
	case _, ok := <-c.send:
		if ok {
			t.Fatal("expected send channel closed after server-side eviction")
		}
	case <-time.After(time.Second):
		t.Fatal("send channel was not closed — client not evicted server-side")
	}
	hub.mu.RLock()
	defer hub.mu.RUnlock()
	if hub.clients[c] {
		t.Fatal("client should have been removed from hub.clients")
	}
}

// The Redis relay's injectEventID round-trips frames through a map (adding
// event_id and reordering keys), so the control frame that reaches a remote
// node is NOT byte-identical to AccountSuspendedFrame(). Interception must
// match structurally, not by bytes.Equal.
func TestFanoutUserSuspendedFrameEvictsAfterRelayMutation(t *testing.T) {
	hub := NewHub()
	c := &Client{hub: hub, userID: "user-y", workspaceID: "ws-1", send: make(chan []byte, 4)}
	hub.mu.Lock()
	hub.clients[c] = true
	hub.mu.Unlock()
	hub.subscribe(c, ScopeUser, "user-y")

	mutated := injectEventID(AccountSuspendedFrame(), "01JEVENTIDULID0000000000")
	if string(mutated) == string(AccountSuspendedFrame()) {
		t.Fatal("test setup: injectEventID did not mutate the frame")
	}
	hub.fanoutUser("user-y", mutated, "", "01JEVENTIDULID0000000000")

	select {
	case _, ok := <-c.send:
		if !ok {
			t.Fatal("send channel closed before the frame was delivered")
		}
	case <-time.After(time.Second):
		t.Fatal("expected the mutated suspended frame to be delivered")
	}
	select {
	case _, ok := <-c.send:
		if ok {
			t.Fatal("expected send channel closed after server-side eviction")
		}
	case <-time.After(time.Second):
		t.Fatal("send channel was not closed — mutated control frame not recognized")
	}
}
