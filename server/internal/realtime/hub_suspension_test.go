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
			if matched, fresh := isAccountSuspendedControlFrame(payload); !matched || !fresh {
				t.Fatalf("payload = %q, want a fresh suspended control frame", payload)
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
		if matched, _ := isAccountSuspendedControlFrame(payload); !matched {
			t.Fatalf("payload = %s, want a suspended control frame", payload)
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

// A suspension control frame can be a relay REPLAY from before a restore.
// The hub verifies the user's CURRENT status: an active (restored) account
// must neither receive the frame (a cooperating client would falsely log
// itself out) nor be evicted.
func TestFanoutUserSuspendedFrameSkipsRestoredAccount(t *testing.T) {
	hub := NewHub()
	hub.SetAccountChecker(fakeAccountChecker{err: nil}) // active again
	c := &Client{hub: hub, userID: "user-z", workspaceID: "ws-1", send: make(chan []byte, 4)}
	hub.mu.Lock()
	hub.clients[c] = true
	hub.mu.Unlock()
	hub.subscribe(c, ScopeUser, "user-z")

	hub.SendToUser("user-z", AccountSuspendedFrame())

	select {
	case payload := <-c.send:
		t.Fatalf("restored account must not receive the suspended frame, got %s", payload)
	default:
	}
	hub.mu.RLock()
	defer hub.mu.RUnlock()
	if !hub.clients[c] {
		t.Fatal("restored account's client must stay registered")
	}
}

// With the checker confirming the suspension, the frame still evicts — and a
// transient checker failure drops the frame rather than falsely kicking.
func TestFanoutUserSuspendedFrameCheckerOutcomes(t *testing.T) {
	newHubClient := func(ac AccountChecker) (*Hub, *Client) {
		hub := NewHub()
		if ac != nil {
			hub.SetAccountChecker(ac)
		}
		c := &Client{hub: hub, userID: "user-w", workspaceID: "ws-1", send: make(chan []byte, 4)}
		hub.mu.Lock()
		hub.clients[c] = true
		hub.mu.Unlock()
		hub.subscribe(c, ScopeUser, "user-w")
		return hub, c
	}

	hub, c := newHubClient(fakeAccountChecker{err: auth.ErrAccountSuspended})
	hub.SendToUser("user-w", AccountSuspendedFrame())
	select {
	case _, ok := <-c.send:
		if !ok {
			t.Fatal("frame should be delivered before eviction")
		}
	case <-time.After(time.Second):
		t.Fatal("confirmed suspension must still deliver + evict")
	}

	hub2, c2 := newHubClient(fakeAccountChecker{err: errors.New("redis down")})
	hub2.SendToUser("user-w", accountSuspendedFrameIssuedAt(time.Now().Add(-10*time.Minute)))
	select {
	case payload := <-c2.send:
		t.Fatalf("transient checker failure must drop a STALE frame, got %s", payload)
	default:
	}
	hub2.mu.RLock()
	if !hub2.clients[c2] {
		hub2.mu.RUnlock()
		t.Fatal("transient checker failure must not evict on a stale frame")
	}
	hub2.mu.RUnlock()
}

// A FRESH suspension frame (just issued by the authoritative suspend path)
// must fail CLOSED when the local status re-check fails transiently: the
// origin node already reported success, so silently dropping the kick would
// lose it with no retry signal. Stale frames (relay replays) keep failing
// open — dropping a replay is safer than falsely kicking an active user.
func TestSuspendedFrameTransientFailureFreshVsStale(t *testing.T) {
	newHubClient := func() (*Hub, *Client) {
		hub := NewHub()
		hub.SetAccountChecker(fakeAccountChecker{err: errors.New("redis down")})
		c := &Client{hub: hub, userID: "user-f", workspaceID: "ws-1", send: make(chan []byte, 4)}
		hub.mu.Lock()
		hub.clients[c] = true
		hub.mu.Unlock()
		hub.subscribe(c, ScopeUser, "user-f")
		return hub, c
	}

	// Fresh frame + transient failure → fail closed: deliver + evict.
	hub, c := newHubClient()
	hub.SendToUser("user-f", AccountSuspendedFrame())
	select {
	case _, ok := <-c.send:
		if !ok {
			t.Fatal("frame should be delivered before eviction")
		}
	case <-time.After(time.Second):
		t.Fatal("fresh frame + transient check failure must fail closed (evict)")
	}

	// Stale frame + transient failure → drop.
	hub2, c2 := newHubClient()
	hub2.SendToUser("user-f", accountSuspendedFrameIssuedAt(time.Now().Add(-10*time.Minute)))
	select {
	case payload := <-c2.send:
		t.Fatalf("stale frame + transient failure must be dropped, got %s", payload)
	default:
	}
	hub2.mu.RLock()
	if !hub2.clients[c2] {
		hub2.mu.RUnlock()
		t.Fatal("stale frame + transient failure must not evict")
	}
	hub2.mu.RUnlock()
}
