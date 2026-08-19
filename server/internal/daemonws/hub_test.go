package daemonws

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/multica-ai/multica/server/internal/realtime"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestNotifyTaskAvailable(t *testing.T) {
	M.Reset()
	defer M.Reset()

	hub := NewHub()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub.HandleWebSocket(w, r, ClientIdentity{RuntimeIDs: []string{"runtime-1"}})
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	deadline := time.Now().Add(time.Second)
	for hub.RuntimeConnectionCount("runtime-1") == 0 {
		if time.Now().After(deadline) {
			t.Fatal("runtime connection was not registered")
		}
		time.Sleep(10 * time.Millisecond)
	}

	hub.NotifyTaskAvailable("runtime-1", "task-1")

	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	_, raw, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}

	var msg protocol.Message
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatalf("unmarshal message: %v", err)
	}
	if msg.Type != protocol.EventDaemonTaskAvailable {
		t.Fatalf("message type = %q, want %q", msg.Type, protocol.EventDaemonTaskAvailable)
	}

	var payload protocol.TaskAvailablePayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.RuntimeID != "runtime-1" || payload.TaskID != "task-1" {
		t.Fatalf("payload = %+v, want runtime/task IDs", payload)
	}
}

func TestNotifyRuntimeProfilesChanged(t *testing.T) {
	M.Reset()
	defer M.Reset()

	hub := NewHub()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub.HandleWebSocket(w, r, ClientIdentity{
			WorkspaceID: "ws-1",
			RuntimeIDs:  []string{"runtime-1"},
		})
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	deadline := time.Now().Add(time.Second)
	for hub.WorkspaceConnectionCount("ws-1") == 0 {
		if time.Now().After(deadline) {
			t.Fatal("workspace connection was not registered")
		}
		time.Sleep(10 * time.Millisecond)
	}

	hub.NotifyRuntimeProfilesChanged("ws-1", "profile-1")

	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	_, raw, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}

	var msg protocol.Message
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatalf("unmarshal message: %v", err)
	}
	if msg.Type != protocol.EventDaemonRuntimeProfilesChanged {
		t.Fatalf("message type = %q, want %q", msg.Type, protocol.EventDaemonRuntimeProfilesChanged)
	}

	var payload protocol.RuntimeProfilesChangedPayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.WorkspaceID != "ws-1" || payload.RuntimeProfileID != "profile-1" {
		t.Fatalf("payload = %+v, want workspace/profile IDs", payload)
	}
}

func TestNotifyWorkspacesChangedSupportsAccountOnlyConnection(t *testing.T) {
	M.Reset()
	defer M.Reset()

	hub := NewHub()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub.HandleWebSocket(w, r, ClientIdentity{UserID: "user-1"})
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	deadline := time.Now().Add(time.Second)
	for hub.UserConnectionCount("user-1") == 0 {
		if time.Now().After(deadline) {
			t.Fatal("user connection was not registered")
		}
		time.Sleep(10 * time.Millisecond)
	}

	hub.NotifyWorkspacesChanged("user-1")

	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	_, raw, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	var msg protocol.Message
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatalf("unmarshal message: %v", err)
	}
	if msg.Type != protocol.EventDaemonWorkspacesChanged {
		t.Fatalf("message type = %q, want %q", msg.Type, protocol.EventDaemonWorkspacesChanged)
	}
}

func TestNotifyRuntimeProfilesChangedIndexesAllAuthorizedWorkspaces(t *testing.T) {
	M.Reset()
	defer M.Reset()

	hub := NewHub()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub.HandleWebSocket(w, r, ClientIdentity{
			WorkspaceIDs: []string{"ws-1", "ws-2"},
			RuntimeIDs:   []string{"runtime-1", "runtime-2"},
		})
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	deadline := time.Now().Add(time.Second)
	for hub.WorkspaceConnectionCount("ws-1") == 0 || hub.WorkspaceConnectionCount("ws-2") == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("workspace connections not registered: ws-1=%d ws-2=%d",
				hub.WorkspaceConnectionCount("ws-1"),
				hub.WorkspaceConnectionCount("ws-2"))
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := hub.WorkspaceConnectionCount("ws-3"); got != 0 {
		t.Fatalf("workspace ws-3 connection count = %d, want 0", got)
	}

	hub.NotifyRuntimeProfilesChanged("ws-1", "profile-1")
	hub.NotifyRuntimeProfilesChanged("ws-2", "profile-2")

	got := map[string]string{}
	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	for len(got) < 2 {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("ReadMessage: %v", err)
		}
		var msg protocol.Message
		if err := json.Unmarshal(raw, &msg); err != nil {
			t.Fatalf("unmarshal message: %v", err)
		}
		if msg.Type != protocol.EventDaemonRuntimeProfilesChanged {
			t.Fatalf("message type = %q, want %q", msg.Type, protocol.EventDaemonRuntimeProfilesChanged)
		}
		var payload protocol.RuntimeProfilesChangedPayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil {
			t.Fatalf("unmarshal payload: %v", err)
		}
		got[payload.WorkspaceID] = payload.RuntimeProfileID
	}
	if got["ws-1"] != "profile-1" || got["ws-2"] != "profile-2" {
		t.Fatalf("profile refresh payloads = %+v, want ws-1/profile-1 and ws-2/profile-2", got)
	}
}

func TestRelayNotifierPublishesDaemonRuntimeScope(t *testing.T) {
	M.Reset()
	defer M.Reset()

	relay := &recordingRelayPublisher{}
	notifier := NewRelayNotifier(nil, relay)

	notifier.NotifyTaskAvailable("runtime-1", "task-1")

	if relay.scopeType != realtime.ScopeDaemonRuntime {
		t.Fatalf("scopeType = %q, want %q", relay.scopeType, realtime.ScopeDaemonRuntime)
	}
	if relay.scopeID != "task-1" {
		t.Fatalf("scopeID = %q, want task_id shard key", relay.scopeID)
	}
	if relay.eventID == "" {
		t.Fatal("expected event id")
	}
	if M.WakeupPublishedTotal.Load() != 1 {
		t.Fatalf("published metric = %d, want 1", M.WakeupPublishedTotal.Load())
	}

	var msg protocol.Message
	if err := json.Unmarshal(relay.frame, &msg); err != nil {
		t.Fatalf("unmarshal frame: %v", err)
	}
	if msg.Type != protocol.EventDaemonTaskAvailable {
		t.Fatalf("message type = %q, want %q", msg.Type, protocol.EventDaemonTaskAvailable)
	}
	var payload protocol.TaskAvailablePayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.RuntimeID != "runtime-1" || payload.TaskID != "task-1" {
		t.Fatalf("payload = %+v, want runtime/task IDs", payload)
	}
}

func TestRelayNotifierPublishesRuntimeProfilesChanged(t *testing.T) {
	M.Reset()
	defer M.Reset()

	relay := &recordingRelayPublisher{}
	notifier := NewRelayNotifier(nil, relay)

	notifier.NotifyRuntimeProfilesChanged("ws-1", "profile-1")

	if relay.scopeType != realtime.ScopeDaemonRuntime {
		t.Fatalf("scopeType = %q, want %q", relay.scopeType, realtime.ScopeDaemonRuntime)
	}
	if relay.scopeID != "ws-1" {
		t.Fatalf("scopeID = %q, want workspace shard key", relay.scopeID)
	}
	if relay.eventID == "" {
		t.Fatal("expected event id")
	}
	if M.WakeupPublishedTotal.Load() != 1 {
		t.Fatalf("published metric = %d, want 1", M.WakeupPublishedTotal.Load())
	}

	var msg protocol.Message
	if err := json.Unmarshal(relay.frame, &msg); err != nil {
		t.Fatalf("unmarshal frame: %v", err)
	}
	if msg.Type != protocol.EventDaemonRuntimeProfilesChanged {
		t.Fatalf("message type = %q, want %q", msg.Type, protocol.EventDaemonRuntimeProfilesChanged)
	}
	var payload protocol.RuntimeProfilesChangedPayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.WorkspaceID != "ws-1" || payload.RuntimeProfileID != "profile-1" {
		t.Fatalf("payload = %+v, want workspace/profile IDs", payload)
	}
}

func TestRelayNotifierPublishesWorkspacesChanged(t *testing.T) {
	M.Reset()
	defer M.Reset()

	relay := &recordingRelayPublisher{}
	notifier := NewRelayNotifier(nil, relay)

	notifier.NotifyWorkspacesChanged("user-1")

	if relay.scopeType != realtime.ScopeDaemonRuntime {
		t.Fatalf("scopeType = %q, want %q", relay.scopeType, realtime.ScopeDaemonRuntime)
	}
	if relay.scopeID != "user-1" {
		t.Fatalf("scopeID = %q, want user shard key", relay.scopeID)
	}
	if relay.eventID == "" {
		t.Fatal("expected event id")
	}
	var msg protocol.Message
	if err := json.Unmarshal(relay.frame, &msg); err != nil {
		t.Fatalf("unmarshal frame: %v", err)
	}
	if msg.Type != protocol.EventDaemonWorkspacesChanged {
		t.Fatalf("message type = %q, want %q", msg.Type, protocol.EventDaemonWorkspacesChanged)
	}
}

func TestRelayNotifierDedupsLocalRedisLoopback(t *testing.T) {
	M.Reset()
	defer M.Reset()

	hub := NewHub()
	client := attachDaemonTestClient(hub, "runtime-1")
	relay := &localFirstDaemonRelayPublisher{t: t, client: client}
	notifier := NewRelayNotifier(hub, relay)

	notifier.NotifyTaskAvailable("runtime-1", "task-1")

	if !relay.called {
		t.Fatal("expected relay publish to be invoked")
	}
	if relay.eventID == "" {
		t.Fatal("expected event id")
	}
	if M.WakeupDeliveredHit.Load() != 1 {
		t.Fatalf("delivered hit metric = %d, want 1", M.WakeupDeliveredHit.Load())
	}

	hub.DeliverDaemonRuntime(relay.scopeID, relay.frame, relay.eventID)

	select {
	case duplicate := <-client.send:
		t.Fatalf("expected redis loopback to be deduped, got duplicate %s", duplicate)
	case <-time.After(20 * time.Millisecond):
	}
	if M.WakeupDeliveredHit.Load() != 1 {
		t.Fatalf("delivered hit metric after loopback = %d, want 1", M.WakeupDeliveredHit.Load())
	}
	if M.WakeupDeliveredMiss.Load() != 0 {
		t.Fatalf("delivered miss metric after dedup = %d, want 0", M.WakeupDeliveredMiss.Load())
	}
}

func TestRelayNotifierDedupsRuntimeProfilesChangedLoopback(t *testing.T) {
	M.Reset()
	defer M.Reset()

	hub := NewHub()
	client := attachDaemonWorkspaceTestClient(hub, "ws-1")
	relay := &localFirstDaemonRelayPublisher{t: t, client: client}
	notifier := NewRelayNotifier(hub, relay)

	notifier.NotifyRuntimeProfilesChanged("ws-1", "profile-1")

	if !relay.called {
		t.Fatal("expected relay publish to be invoked")
	}
	if relay.eventID == "" {
		t.Fatal("expected event id")
	}
	if M.WakeupDeliveredHit.Load() != 0 {
		t.Fatalf("delivered hit metric = %d, want 0 before redis relay delivery", M.WakeupDeliveredHit.Load())
	}

	hub.DeliverDaemonRuntime(relay.scopeID, relay.frame, relay.eventID)

	select {
	case duplicate := <-client.send:
		t.Fatalf("expected redis loopback to be deduped, got duplicate %s", duplicate)
	case <-time.After(20 * time.Millisecond):
	}
	if M.WakeupDeliveredHit.Load() != 0 {
		t.Fatalf("delivered hit metric after loopback = %d, want 0", M.WakeupDeliveredHit.Load())
	}
	if M.WakeupDeliveredMiss.Load() != 0 {
		t.Fatalf("delivered miss metric after dedup = %d, want 0", M.WakeupDeliveredMiss.Load())
	}
}

func TestRelayNotifierDedupsWorkspacesChangedLoopback(t *testing.T) {
	M.Reset()
	defer M.Reset()

	hub := NewHub()
	client := attachDaemonUserTestClient(hub, "user-1")
	relay := &localFirstDaemonRelayPublisher{t: t, client: client}
	notifier := NewRelayNotifier(hub, relay)

	notifier.NotifyWorkspacesChanged("user-1")
	if !relay.called || relay.eventID == "" {
		t.Fatal("expected local delivery followed by relay publish")
	}

	hub.DeliverDaemonRuntime(relay.scopeID, relay.frame, relay.eventID)
	select {
	case duplicate := <-client.send:
		t.Fatalf("expected redis loopback to be deduped, got duplicate %s", duplicate)
	case <-time.After(20 * time.Millisecond):
	}
}

// TestHeartbeatRoundTrip pins the WS heartbeat contract: a daemon:heartbeat
// frame invokes the registered HeartbeatHandler with the runtime ID, and the
// hub serializes the returned ack as a daemon:heartbeat_ack on the wire.
func TestHeartbeatRoundTrip(t *testing.T) {
	M.Reset()
	defer M.Reset()

	hub := NewHub()
	var calls atomic.Int32
	hub.SetHeartbeatHandler(func(_ context.Context, identity ClientIdentity, runtimeID string, _ bool) (*protocol.DaemonHeartbeatAckPayload, error) {
		calls.Add(1)
		if identity.WorkspaceID != "ws-1" {
			t.Errorf("identity workspace = %q, want ws-1", identity.WorkspaceID)
		}
		return &protocol.DaemonHeartbeatAckPayload{
			RuntimeID: runtimeID,
			Status:    "ok",
			PendingUpdate: &protocol.DaemonHeartbeatPendingUpdate{
				ID:            "update-1",
				TargetVersion: "0.1.99",
			},
		}, nil
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub.HandleWebSocket(w, r, ClientIdentity{
			WorkspaceID: "ws-1",
			RuntimeIDs:  []string{"runtime-1"},
		})
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	hbFrame, err := json.Marshal(protocol.Message{
		Type:    protocol.EventDaemonHeartbeat,
		Payload: mustMarshalRaw(protocol.DaemonHeartbeatRequestPayload{RuntimeID: "runtime-1"}),
	})
	if err != nil {
		t.Fatalf("marshal heartbeat: %v", err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, hbFrame); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}

	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	_, raw, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}

	var msg protocol.Message
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatalf("unmarshal ack envelope: %v", err)
	}
	if msg.Type != protocol.EventDaemonHeartbeatAck {
		t.Fatalf("ack type = %q, want %q", msg.Type, protocol.EventDaemonHeartbeatAck)
	}
	var ack protocol.DaemonHeartbeatAckPayload
	if err := json.Unmarshal(msg.Payload, &ack); err != nil {
		t.Fatalf("unmarshal ack payload: %v", err)
	}
	if ack.RuntimeID != "runtime-1" {
		t.Fatalf("ack runtime_id = %q, want runtime-1", ack.RuntimeID)
	}
	if ack.PendingUpdate == nil || ack.PendingUpdate.ID != "update-1" {
		t.Fatalf("ack pending_update = %+v, want update-1", ack.PendingUpdate)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("HeartbeatHandler invocations = %d, want 1", got)
	}
}

// TestHeartbeatHandlerCtxNotTimeBounded pins the PopPending invariant: the
// hub must not wrap the handler ctx with a short WithTimeout, otherwise the
// Redis Lua claim script can be cancelled mid-flight after its side effects
// have already landed. We assert by stalling the handler past any timeout
// the hub might be tempted to add and verifying the ack still arrives.
func TestHeartbeatHandlerCtxNotTimeBounded(t *testing.T) {
	M.Reset()
	defer M.Reset()

	hub := NewHub()
	const stall = 250 * time.Millisecond
	hub.SetHeartbeatHandler(func(ctx context.Context, _ ClientIdentity, runtimeID string, _ bool) (*protocol.DaemonHeartbeatAckPayload, error) {
		select {
		case <-time.After(stall):
		case <-ctx.Done():
			t.Errorf("handler ctx was cancelled (deadline=%v) — PopPending invariant violated", ctx.Err())
			return nil, ctx.Err()
		}
		if _, ok := ctx.Deadline(); ok {
			t.Errorf("handler ctx must not carry a deadline; PopPending side effects cannot be safely un-run")
		}
		return &protocol.DaemonHeartbeatAckPayload{RuntimeID: runtimeID, Status: "ok"}, nil
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub.HandleWebSocket(w, r, ClientIdentity{RuntimeIDs: []string{"runtime-1"}})
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	hbFrame, err := json.Marshal(protocol.Message{
		Type:    protocol.EventDaemonHeartbeat,
		Payload: mustMarshalRaw(protocol.DaemonHeartbeatRequestPayload{RuntimeID: "runtime-1"}),
	})
	if err != nil {
		t.Fatalf("marshal heartbeat: %v", err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, hbFrame); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}

	if err := conn.SetReadDeadline(time.Now().Add(stall + 2*time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	_, raw, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	var msg protocol.Message
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatalf("unmarshal ack: %v", err)
	}
	if msg.Type != protocol.EventDaemonHeartbeatAck {
		t.Fatalf("ack type = %q, want %q", msg.Type, protocol.EventDaemonHeartbeatAck)
	}
}

// TestHeartbeatRejectsUnauthorizedRuntime verifies that a heartbeat for a
// runtime outside the connection's authenticated set is dropped silently —
// no handler call, no ack frame.
func TestHeartbeatRejectsUnauthorizedRuntime(t *testing.T) {
	M.Reset()
	defer M.Reset()

	hub := NewHub()
	var called atomic.Bool
	hub.SetHeartbeatHandler(func(context.Context, ClientIdentity, string, bool) (*protocol.DaemonHeartbeatAckPayload, error) {
		called.Store(true)
		return &protocol.DaemonHeartbeatAckPayload{Status: "ok"}, nil
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub.HandleWebSocket(w, r, ClientIdentity{RuntimeIDs: []string{"runtime-1"}})
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	hbFrame, err := json.Marshal(protocol.Message{
		Type:    protocol.EventDaemonHeartbeat,
		Payload: mustMarshalRaw(protocol.DaemonHeartbeatRequestPayload{RuntimeID: "runtime-other"}),
	})
	if err != nil {
		t.Fatalf("marshal heartbeat: %v", err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, hbFrame); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}

	if err := conn.SetReadDeadline(time.Now().Add(150 * time.Millisecond)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatalf("expected no ack for unauthorized runtime, got message")
	}
	if called.Load() {
		t.Fatalf("HeartbeatHandler invoked for unauthorized runtime")
	}
}

func attachDaemonTestClient(hub *Hub, runtimeID string) *client {
	c := &client{
		send:     make(chan []byte, 2),
		runtimes: map[string]struct{}{runtimeID: {}},
	}

	hub.mu.Lock()
	hub.clients[c] = true
	hub.byRuntime[runtimeID] = map[*client]bool{c: true}
	hub.mu.Unlock()

	return c
}

func attachDaemonWorkspaceTestClient(hub *Hub, workspaceID string) *client {
	c := &client{
		send:     make(chan []byte, 2),
		identity: ClientIdentity{WorkspaceIDs: []string{workspaceID}},
		runtimes: map[string]struct{}{},
	}

	hub.mu.Lock()
	hub.clients[c] = true
	hub.byWorkspace[workspaceID] = map[*client]bool{c: true}
	hub.mu.Unlock()

	return c
}

func attachDaemonUserTestClient(hub *Hub, userID string) *client {
	c := &client{
		send:     make(chan []byte, 2),
		identity: ClientIdentity{UserID: userID},
		runtimes: map[string]struct{}{},
	}

	hub.mu.Lock()
	hub.clients[c] = true
	hub.byUser[userID] = map[*client]bool{c: true}
	hub.mu.Unlock()

	return c
}

type recordingRelayPublisher struct {
	scopeType string
	scopeID   string
	exclude   string
	frame     []byte
	eventID   string
}

func (r *recordingRelayPublisher) PublishWithID(scopeType, scopeID, exclude string, frame []byte, id string) error {
	r.scopeType = scopeType
	r.scopeID = scopeID
	r.exclude = exclude
	r.frame = append([]byte(nil), frame...)
	r.eventID = id
	return nil
}

type localFirstDaemonRelayPublisher struct {
	t      *testing.T
	client *client

	called     bool
	scopeType  string
	scopeID    string
	exclude    string
	frame      []byte
	eventID    string
	localFrame []byte
}

func (p *localFirstDaemonRelayPublisher) PublishWithID(scopeType, scopeID, exclude string, frame []byte, id string) error {
	p.called = true
	p.scopeType = scopeType
	p.scopeID = scopeID
	p.exclude = exclude
	p.frame = append([]byte(nil), frame...)
	p.eventID = id

	select {
	case p.localFrame = <-p.client.send:
	default:
		p.t.Fatal("expected local fanout to happen before relay publish")
	}
	return nil
}

// Account suspension revokes the daemon's mdt_ token, but a token only gates
// NEW connections — an already-established daemon WebSocket keeps serving
// heartbeats and RPCs (tasks.claim) with its cached identity. Suspension must
// therefore also sever the live sockets watching the suspended user's
// runtimes, while daemons on other runtimes stay connected.
func TestDisconnectRuntimesSeversOnlyMatchingConnections(t *testing.T) {
	M.Reset()
	defer M.Reset()

	hub := NewHub()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		runtimeID := r.URL.Query().Get("rt")
		hub.HandleWebSocket(w, r, ClientIdentity{RuntimeIDs: []string{runtimeID}})
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	suspended, _, err := websocket.DefaultDialer.Dial(wsURL+"?rt=runtime-suspended", nil)
	if err != nil {
		t.Fatalf("Dial suspended: %v", err)
	}
	defer suspended.Close()
	bystander, _, err := websocket.DefaultDialer.Dial(wsURL+"?rt=runtime-bystander", nil)
	if err != nil {
		t.Fatalf("Dial bystander: %v", err)
	}
	defer bystander.Close()

	deadline := time.Now().Add(time.Second)
	for hub.RuntimeConnectionCount("runtime-suspended") == 0 ||
		hub.RuntimeConnectionCount("runtime-bystander") == 0 {
		if time.Now().After(deadline) {
			t.Fatal("connections were not registered")
		}
		time.Sleep(10 * time.Millisecond)
	}

	hub.DisconnectRuntimes([]string{"runtime-suspended"})

	// The suspended runtime's socket must observe a close/read error.
	suspended.SetReadDeadline(time.Now().Add(time.Second))
	if _, _, err := suspended.ReadMessage(); err == nil {
		t.Fatal("expected suspended runtime's connection to be closed")
	}
	deadline = time.Now().Add(time.Second)
	for hub.RuntimeConnectionCount("runtime-suspended") != 0 {
		if time.Now().After(deadline) {
			t.Fatal("suspended runtime's connection was not unregistered")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// The bystander stays registered and its socket stays healthy.
	if hub.RuntimeConnectionCount("runtime-bystander") != 1 {
		t.Fatal("bystander connection should be untouched")
	}
	hub.NotifyTaskAvailable("runtime-bystander", "task-1")
	bystander.SetReadDeadline(time.Now().Add(time.Second))
	if _, _, err := bystander.ReadMessage(); err != nil {
		t.Fatalf("bystander read after DisconnectRuntimes: %v", err)
	}
}

// Cross-node daemon kick: suspension on node A must sever daemon sockets held
// by node B. The revocation rides the existing ScopeDaemonRuntime relay as a
// typed frame; each node's DeliverDaemonRuntime dispatch recognizes it and
// evicts locally instead of forwarding it to the daemon.
func TestDeliverDaemonRuntimeRevokedFrameSeversConnections(t *testing.T) {
	M.Reset()
	defer M.Reset()

	hub := NewHub()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub.HandleWebSocket(w, r, ClientIdentity{RuntimeIDs: []string{"runtime-revoked"}})
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	deadline := time.Now().Add(time.Second)
	for hub.RuntimeConnectionCount("runtime-revoked") == 0 {
		if time.Now().After(deadline) {
			t.Fatal("connection was not registered")
		}
		time.Sleep(10 * time.Millisecond)
	}

	frame, err := runtimesRevokedFrame([]string{"runtime-revoked"})
	if err != nil {
		t.Fatalf("runtimesRevokedFrame: %v", err)
	}
	hub.DeliverDaemonRuntime("runtime-revoked", frame, "event-1")

	conn.SetReadDeadline(time.Now().Add(time.Second))
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatal("expected revoked runtime's connection to be closed")
	}
	deadline = time.Now().Add(time.Second)
	for hub.RuntimeConnectionCount("runtime-revoked") != 0 {
		if time.Now().After(deadline) {
			t.Fatal("revoked runtime's connection was not unregistered")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Suspension revocations must reach every node regardless of relay mode, so
// they publish to the FIXED daemon control scope (which even the legacy
// relay consumes unconditionally) rather than a per-runtime shard key whose
// stream may have no consumer.
func TestDisconnectRuntimesPublishesControlScope(t *testing.T) {
	M.Reset()
	defer M.Reset()

	relay := &recordingRelayPublisher{}
	notifier := NewRelayNotifier(nil, relay)

	if err := notifier.DisconnectRuntimes([]string{"rt-1", "rt-2"}); err != nil {
		t.Fatalf("DisconnectRuntimes: %v", err)
	}
	if relay.scopeType != realtime.ScopeDaemonRuntime {
		t.Fatalf("scopeType = %q, want %q", relay.scopeType, realtime.ScopeDaemonRuntime)
	}
	if relay.scopeID != realtime.DaemonControlScopeID {
		t.Fatalf("scopeID = %q, want fixed control scope %q", relay.scopeID, realtime.DaemonControlScopeID)
	}
}

// A revocation is a connection-lifecycle event: every socket this node holds
// postdates process start, and a reconnect after a genuine revocation is
// rejected by per-request token authority anyway. A replayed frame issued
// BEFORE this hub started (relay replay on node restart) can therefore only
// hit re-paired, legitimate connections — it must be dropped.
func TestDeliverDaemonRuntimeRevokedReplayFromBeforeStartDropped(t *testing.T) {
	M.Reset()
	defer M.Reset()

	hub := NewHub()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub.HandleWebSocket(w, r, ClientIdentity{RuntimeIDs: []string{"runtime-replay"}})
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	deadline := time.Now().Add(time.Second)
	for hub.RuntimeConnectionCount("runtime-replay") == 0 {
		if time.Now().After(deadline) {
			t.Fatal("connection was not registered")
		}
		time.Sleep(10 * time.Millisecond)
	}

	stale, err := runtimesRevokedFrameAt([]string{"runtime-replay"}, hub.startedAt.Add(-time.Minute))
	if err != nil {
		t.Fatalf("runtimesRevokedFrameAt: %v", err)
	}
	hub.DeliverDaemonRuntime("runtime-replay", stale, "event-replay")

	// The connection must survive a stale replay.
	time.Sleep(50 * time.Millisecond)
	if hub.RuntimeConnectionCount("runtime-replay") != 1 {
		t.Fatal("stale revocation replay must not sever the connection")
	}
}
