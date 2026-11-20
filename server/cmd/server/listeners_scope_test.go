package main

import (
	"encoding/json"
	"sync"
	"testing"

	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/realtime"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// fakeBroadcaster records every fanout call so tests can assert which scope a
// given event landed on.
type fakeBroadcaster struct {
	mu              sync.Mutex
	scopeCalls      []scopeCall
	workspaceCalls  []workspaceCall
	userCalls       []userCall
	broadcastCalled int
}

func TestRegisterListeners_ChatSessionCreatedGoesOnlyToCreator(t *testing.T) {
	bus := events.New()
	fb := &fakeBroadcaster{}
	registerListeners(bus, fb)

	bus.Publish(events.Event{
		Type: protocol.EventChatSessionCreated, WorkspaceID: "ws-1",
		ActorType: "member", ActorID: "creator-1", ChatSessionID: "chat-1", ChatSessionOwnerID: "creator-1",
		Payload: protocol.ChatSessionCreatedPayload{
			WorkspaceID: "ws-1", ChatSessionID: "chat-1", CreatorID: "creator-1",
			Title: "private opening title",
		},
	})

	msg := onlyOwnerWorkspaceFrame(t, fb, "ws-1", "creator-1")
	var frame struct {
		Payload protocol.ChatSessionCreatedPayload `json:"payload"`
	}
	if err := json.Unmarshal(msg, &frame); err != nil {
		t.Fatalf("decode creator frame: %v", err)
	}
	if frame.Payload.WorkspaceID != "ws-1" || frame.Payload.ChatSessionID != "chat-1" {
		t.Fatalf("creator payload = %+v", frame.Payload)
	}
}

func TestRegisterListeners_ChatSessionTitleUpdateGoesOnlyToCreator(t *testing.T) {
	bus := events.New()
	fb := &fakeBroadcaster{}
	registerListeners(bus, fb)

	bus.Publish(events.Event{
		Type: protocol.EventChatSessionUpdated, WorkspaceID: "ws-1",
		ActorType: "member", ActorID: "creator-1", ChatSessionID: "chat-1", ChatSessionOwnerID: "creator-1",
		Payload: protocol.ChatSessionUpdatedPayload{
			ChatSessionID: "chat-1", Title: "private derived title",
		},
	})

	onlyOwnerWorkspaceFrame(t, fb, "ws-1", "creator-1")
}

// onlyOwnerWorkspaceFrame asserts that the one delivery went to ownerID's
// connections bound to workspaceID, and returns its frame. The user scope
// would also reach the owner's connections to other workspaces, which are not
// re-authorized against workspaceID.
func onlyOwnerWorkspaceFrame(t *testing.T, fb *fakeBroadcaster, workspaceID, ownerID string) []byte {
	t.Helper()
	fb.mu.Lock()
	defer fb.mu.Unlock()
	if len(fb.workspaceCalls) != 0 || len(fb.userCalls) != 0 || fb.broadcastCalled != 0 {
		t.Fatalf("private content left the owner's workspace connections: workspace=%+v user=%+v broadcast=%d",
			fb.workspaceCalls, fb.userCalls, fb.broadcastCalled)
	}
	want := realtime.WorkspaceMemberScopeID(workspaceID, ownerID)
	if len(fb.scopeCalls) != 1 || fb.scopeCalls[0].scopeType != realtime.ScopeWorkspaceMember || fb.scopeCalls[0].scopeID != want {
		t.Fatalf("scope fanout = %+v, want %s/%s once", fb.scopeCalls, realtime.ScopeWorkspaceMember, want)
	}
	return fb.scopeCalls[0].msg
}

type scopeCall struct {
	scopeType, scopeID string
	msg                []byte
}
type workspaceCall struct {
	workspaceID string
	msg         []byte
}
type userCall struct {
	userID  string
	msg     []byte
	exclude []string
}

func (f *fakeBroadcaster) BroadcastToScope(scopeType, scopeID string, message []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scopeCalls = append(f.scopeCalls, scopeCall{scopeType, scopeID, message})
}
func (f *fakeBroadcaster) BroadcastToWorkspace(workspaceID string, message []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.workspaceCalls = append(f.workspaceCalls, workspaceCall{workspaceID, message})
}
func (f *fakeBroadcaster) SendToUser(userID string, message []byte, excludeWorkspace ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.userCalls = append(f.userCalls, userCall{userID, message, excludeWorkspace})
}
func (f *fakeBroadcaster) Broadcast(message []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.broadcastCalled++
}

func TestRegisterListeners_InternalAuthorizationExpansionNeverBroadcasts(t *testing.T) {
	bus := events.New()
	fb := &fakeBroadcaster{}
	registerListeners(bus, fb)

	bus.Publish(events.Event{
		Type:        events.EventWorkspaceAuthorizationExpanded,
		WorkspaceID: "ws-1",
	})

	if len(fb.scopeCalls) != 1 {
		t.Fatalf("authorization expansion scope calls = %+v, want one", fb.scopeCalls)
	}
	if got := fb.scopeCalls[0]; got.scopeType != realtime.ScopeWorkspaceAuthorization || got.scopeID != "ws-1" {
		t.Fatalf("authorization expansion scope = %s:%s, want %s:ws-1", got.scopeType, got.scopeID, realtime.ScopeWorkspaceAuthorization)
	}
	if len(fb.workspaceCalls) != 0 {
		t.Fatalf("internal authorization expansion leaked to workspace clients: %+v", fb.workspaceCalls)
	}
}

func TestRegisterListeners_TaskContentUsesTaskScope(t *testing.T) {
	cases := []struct {
		name      string
		eventType string
		taskID    string
	}{
		{"task:message", protocol.EventTaskMessage, "task-1"},
		{"task:progress", protocol.EventTaskProgress, "task-2"},
		{"task:activity", protocol.EventTaskActivity, "task-3"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bus := events.New()
			fb := &fakeBroadcaster{}
			registerListeners(bus, fb)

			bus.Publish(events.Event{
				Type:        tc.eventType,
				WorkspaceID: "ws-1",
				TaskID:      tc.taskID,
				AgentID:     "agent-1",
				Payload:     map[string]any{"task_id": tc.taskID},
			})

			if len(fb.scopeCalls) != 2 {
				t.Fatalf("BroadcastToScope calls = %d, want task scope plus legacy compatibility scope", len(fb.scopeCalls))
			}
			if fb.scopeCalls[0].scopeType != "task" || fb.scopeCalls[0].scopeID != tc.taskID {
				t.Fatalf("task event scope = %s:%s, want task:%s", fb.scopeCalls[0].scopeType, fb.scopeCalls[0].scopeID, tc.taskID)
			}
			wantLegacyID := realtime.WorkspaceAgentScopeID("ws-1", "agent-1")
			if got := fb.scopeCalls[1]; got.scopeType != realtime.ScopeLegacyWorkspaceAgent || got.scopeID != wantLegacyID {
				t.Fatalf("legacy task event scope = %s:%s, want %s:%s", got.scopeType, got.scopeID, realtime.ScopeLegacyWorkspaceAgent, wantLegacyID)
			}
			if len(fb.workspaceCalls) != 0 {
				t.Fatalf("task content leaked to %d workspace broadcast(s)", len(fb.workspaceCalls))
			}
		})
	}
}

func TestRegisterListeners_ChatTaskContentUsesCreatorLegacyScope(t *testing.T) {
	bus := events.New()
	fb := &fakeBroadcaster{}
	registerListeners(bus, fb)

	bus.Publish(events.Event{
		Type:            protocol.EventTaskMessage,
		WorkspaceID:     "ws-1",
		TaskID:          "task-1",
		AgentID:         "agent-1",
		ChatSessionID:   "chat-1",
		RecipientUserID: "creator-1",
		Payload:         map[string]any{"task_id": "task-1", "content": "private"},
	})

	if len(fb.scopeCalls) != 2 {
		t.Fatalf("scope calls = %+v, want task scope plus legacy creator scope", fb.scopeCalls)
	}
	wantLegacyID := realtime.UserAgentScopeID("creator-1", "agent-1")
	if got := fb.scopeCalls[1]; got.scopeType != realtime.ScopeLegacyUserAgent || got.scopeID != wantLegacyID {
		t.Fatalf("legacy chat task scope = %s:%s, want %s:%s", got.scopeType, got.scopeID, realtime.ScopeLegacyUserAgent, wantLegacyID)
	}
}

func TestRegisterListeners_TaskContentWithoutTrustedTaskIDDrops(t *testing.T) {
	bus := events.New()
	fb := &fakeBroadcaster{}
	registerListeners(bus, fb)

	bus.Publish(events.Event{
		Type:        protocol.EventTaskMessage,
		WorkspaceID: "ws-1",
		Payload:     map[string]any{"task_id": "payload-only", "content": "secret"},
	})

	if len(fb.scopeCalls) != 0 || len(fb.workspaceCalls) != 0 {
		t.Fatalf("task content without trusted routing metadata was delivered: scopes=%+v workspaces=%+v", fb.scopeCalls, fb.workspaceCalls)
	}
}

func TestRegisterListeners_ChatContentUsesTrustedRecipient(t *testing.T) {
	bus := events.New()
	fb := &fakeBroadcaster{}
	registerListeners(bus, fb)

	bus.Publish(events.Event{
		Type:            protocol.EventChatDone,
		WorkspaceID:     "ws-1",
		AgentID:         "agent-1",
		RecipientUserID: "creator-1",
		Payload:         map[string]any{"chat_session_id": "chat-1", "content": "private reply"},
	})

	if len(fb.scopeCalls) != 1 {
		t.Fatalf("chat visibility-scope calls = %+v, want one", fb.scopeCalls)
	}
	wantID := realtime.UserAgentScopeID("creator-1", "agent-1")
	if got := fb.scopeCalls[0]; got.scopeType != realtime.ScopeUserAgent || got.scopeID != wantID {
		t.Fatalf("chat scope = %s:%s, want %s:%s", got.scopeType, got.scopeID, realtime.ScopeUserAgent, wantID)
	}
	if len(fb.workspaceCalls) != 0 || len(fb.userCalls) != 0 {
		t.Fatalf("chat content escaped creator-Agent routing: workspaces=%+v users=%+v", fb.workspaceCalls, fb.userCalls)
	}
}

func TestRegisterListeners_ChatContentWithoutTrustedRecipientDrops(t *testing.T) {
	bus := events.New()
	fb := &fakeBroadcaster{}
	registerListeners(bus, fb)

	bus.Publish(events.Event{
		Type:        protocol.EventChatMessage,
		WorkspaceID: "ws-1",
		AgentID:     "agent-1",
		Payload:     map[string]any{"chat_session_id": "chat-1", "content": "private prompt"},
	})

	if len(fb.userCalls) != 0 || len(fb.workspaceCalls) != 0 || len(fb.scopeCalls) != 0 {
		t.Fatalf("chat content without trusted recipient was delivered: users=%+v workspaces=%+v scopes=%+v", fb.userCalls, fb.workspaceCalls, fb.scopeCalls)
	}
}

func TestRegisterListeners_TaskLifecycleUsesVisibilityScope(t *testing.T) {
	t.Run("issue task uses workspace-agent scope", func(t *testing.T) {
		bus := events.New()
		fb := &fakeBroadcaster{}
		registerListeners(bus, fb)

		bus.Publish(events.Event{
			Type:        protocol.EventTaskRunning,
			WorkspaceID: "ws-1",
			AgentID:     "agent-1",
			IssueID:     "issue-1",
			TaskID:      "task-1",
			Payload:     map[string]any{"task_id": "task-1", "issue_id": "issue-1", "status": "running"},
		})

		if len(fb.scopeCalls) != 1 {
			t.Fatalf("scope calls = %d, want 1", len(fb.scopeCalls))
		}
		wantID := realtime.WorkspaceAgentScopeID("ws-1", "agent-1")
		if got := fb.scopeCalls[0]; got.scopeType != realtime.ScopeWorkspaceAgent || got.scopeID != wantID {
			t.Fatalf("lifecycle scope = %s:%s, want %s:%s", got.scopeType, got.scopeID, realtime.ScopeWorkspaceAgent, wantID)
		}
		if len(fb.workspaceCalls) != 0 {
			t.Fatalf("lifecycle leaked to workspace: %+v", fb.workspaceCalls)
		}
	})

	t.Run("direct chat task uses creator-agent scope", func(t *testing.T) {
		bus := events.New()
		fb := &fakeBroadcaster{}
		registerListeners(bus, fb)

		bus.Publish(events.Event{
			Type:            protocol.EventTaskQueued,
			WorkspaceID:     "ws-1",
			AgentID:         "agent-1",
			TaskID:          "task-1",
			ChatSessionID:   "chat-1",
			RecipientUserID: "creator-1",
			Payload:         map[string]any{"task_id": "task-1", "chat_session_id": "chat-1", "status": "queued"},
		})

		if len(fb.scopeCalls) != 1 {
			t.Fatalf("scope calls = %d, want 1", len(fb.scopeCalls))
		}
		wantID := realtime.UserAgentScopeID("creator-1", "agent-1")
		if got := fb.scopeCalls[0]; got.scopeType != realtime.ScopeUserAgent || got.scopeID != wantID {
			t.Fatalf("chat lifecycle scope = %s:%s, want %s:%s", got.scopeType, got.scopeID, realtime.ScopeUserAgent, wantID)
		}
		if len(fb.workspaceCalls) != 0 || len(fb.userCalls) != 0 {
			t.Fatalf("chat lifecycle escaped user-agent routing: workspaces=%+v users=%+v", fb.workspaceCalls, fb.userCalls)
		}
	})
}

func TestRegisterListeners_TaskLifecycleWithoutTrustedAgentIDDrops(t *testing.T) {
	bus := events.New()
	fb := &fakeBroadcaster{}
	registerListeners(bus, fb)

	bus.Publish(events.Event{
		Type:        protocol.EventTaskCompleted,
		WorkspaceID: "ws-1",
		TaskID:      "task-1",
		Payload:     map[string]any{"task_id": "task-1", "status": "completed"},
	})

	if len(fb.scopeCalls) != 0 || len(fb.workspaceCalls) != 0 || len(fb.userCalls) != 0 {
		t.Fatalf("lifecycle without trusted Agent id was delivered: scopes=%+v workspaces=%+v users=%+v", fb.scopeCalls, fb.workspaceCalls, fb.userCalls)
	}
}

func TestRegisterListeners_VisibilityChangesInvalidateWorkspaceAuthorization(t *testing.T) {
	for _, tc := range []struct {
		name  string
		event events.Event
	}{
		{name: "Agent archived", event: events.Event{Type: protocol.EventAgentArchived, WorkspaceID: "ws-1"}},
		{name: "Agent restored", event: events.Event{Type: protocol.EventAgentRestored, WorkspaceID: "ws-1"}},
		{name: "member role updated", event: events.Event{Type: protocol.EventMemberUpdated, WorkspaceID: "ws-1"}},
		{name: "member removed", event: events.Event{Type: protocol.EventMemberRemoved, WorkspaceID: "ws-1"}},
		{name: "Agent permission updated", event: events.Event{Type: protocol.EventAgentStatus, WorkspaceID: "ws-1", AuthorizationChanged: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bus := events.New()
			fb := &fakeBroadcaster{}
			registerListeners(bus, fb)
			bus.Publish(tc.event)

			var found bool
			for _, call := range fb.scopeCalls {
				if call.scopeType == realtime.ScopeWorkspaceAuthorization && call.scopeID == "ws-1" {
					found = true
				}
			}
			if !found {
				t.Fatalf("authorization invalidation was not published: %+v", fb.scopeCalls)
			}
		})
	}
}

func TestRegisterListeners_AgentCreatedDoesNotDisconnectWorkspace(t *testing.T) {
	bus := events.New()
	fb := &fakeBroadcaster{}
	registerListeners(bus, fb)
	bus.Publish(events.Event{Type: protocol.EventAgentCreated, WorkspaceID: "ws-1"})

	var expansion bool
	for _, call := range fb.scopeCalls {
		if call.scopeType == realtime.ScopeWorkspaceAuthorization && string(call.msg) == string(realtime.AuthorizationExpandedFrame()) {
			expansion = true
		}
	}
	if !expansion {
		t.Fatalf("Agent creation did not request in-place authorization expansion: %+v", fb.scopeCalls)
	}
}

func TestRegisterListeners_UnknownWorkspaceEventFailsClosed(t *testing.T) {
	bus := events.New()
	fb := &fakeBroadcaster{}
	registerListeners(bus, fb)
	bus.Publish(events.Event{Type: "future:unclassified", WorkspaceID: "ws-1", Payload: map[string]any{"secret": "no"}})

	if len(fb.scopeCalls) != 0 || len(fb.workspaceCalls) != 0 || len(fb.userCalls) != 0 || fb.broadcastCalled != 0 {
		t.Fatalf("unclassified workspace event was delivered: scopes=%+v workspaces=%+v users=%+v global=%d", fb.scopeCalls, fb.workspaceCalls, fb.userCalls, fb.broadcastCalled)
	}
}

// An invitation conclusion must reach the invitee BOTH ways: the workspace
// broadcast keeps admin pending lists fresh, and a targeted send reaches the
// invitee's own clients — which are usually bound to a different workspace
// room (or no room at all, #8432), so the broadcast alone never arrives.
// The targeted frame passes excludeWorkspace so clients already in the room
// don't get the event twice.
func TestRegisterListeners_InvitationConcludedReachesActorAndWorkspace(t *testing.T) {
	cases := []struct {
		name      string
		eventType string
	}{
		{"invitation:accepted", protocol.EventInvitationAccepted},
		{"invitation:declined", protocol.EventInvitationDeclined},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bus := events.New()
			fb := &fakeBroadcaster{}
			registerListeners(bus, fb)

			bus.Publish(events.Event{
				Type: tc.eventType, WorkspaceID: "ws-1",
				ActorType: "member", ActorID: "invitee-1",
				Payload: map[string]any{"invitation_id": "inv-1"},
			})

			if len(fb.workspaceCalls) != 1 || fb.workspaceCalls[0].workspaceID != "ws-1" {
				t.Fatalf("workspace fanout = %+v, want exactly ws-1 (admin lists depend on it)", fb.workspaceCalls)
			}
			if len(fb.userCalls) != 1 {
				t.Fatalf("targeted sends = %+v, want exactly one", fb.userCalls)
			}
			send := fb.userCalls[0]
			if send.userID != "invitee-1" {
				t.Fatalf("targeted recipient = %q, want the acting invitee invitee-1", send.userID)
			}
			if len(send.exclude) != 1 || send.exclude[0] != "ws-1" {
				t.Fatalf("excludeWorkspace = %v, want [ws-1] to dedupe the room broadcast", send.exclude)
			}
			var frame struct {
				Type    string `json:"type"`
				ActorID string `json:"actor_id"`
				Payload struct {
					InvitationID string `json:"invitation_id"`
				} `json:"payload"`
			}
			if err := json.Unmarshal(send.msg, &frame); err != nil {
				t.Fatalf("decode targeted frame: %v", err)
			}
			if frame.Type != tc.eventType || frame.ActorID != "invitee-1" || frame.Payload.InvitationID != "inv-1" {
				t.Fatalf("targeted frame = type %q actor %q payload %+v", frame.Type, frame.ActorID, frame.Payload)
			}
		})
	}
}

// Without an acting invitee there is nobody to target; the workspace
// broadcast must still go out.
func TestRegisterListeners_InvitationConcludedWithoutActorSkipsTargetedSend(t *testing.T) {
	bus := events.New()
	fb := &fakeBroadcaster{}
	registerListeners(bus, fb)

	bus.Publish(events.Event{
		Type: protocol.EventInvitationDeclined, WorkspaceID: "ws-1",
		ActorType: "member",
		Payload:   map[string]any{"invitation_id": "inv-1"},
	})

	if len(fb.workspaceCalls) != 1 {
		t.Fatalf("workspace fanout = %+v, want exactly one", fb.workspaceCalls)
	}
	if len(fb.userCalls) != 0 {
		t.Fatalf("targeted sends = %+v, want none without an actor", fb.userCalls)
	}
}

// invitation:revoked keeps its invitee_user_id routing: its actor is the
// revoking admin, so targeting the actor would refresh the wrong user's list.
func TestRegisterListeners_InvitationRevokedStillTargetsInviteeNotActor(t *testing.T) {
	bus := events.New()
	fb := &fakeBroadcaster{}
	registerListeners(bus, fb)

	bus.Publish(events.Event{
		Type: protocol.EventInvitationRevoked, WorkspaceID: "ws-1",
		ActorType: "member", ActorID: "admin-1",
		Payload: map[string]any{"invitee_user_id": inviteePtr("invitee-2")},
	})

	if len(fb.workspaceCalls) != 0 {
		t.Fatalf("revoked reached workspace fanout: %+v (still a personal event)", fb.workspaceCalls)
	}
	if len(fb.userCalls) != 1 || fb.userCalls[0].userID != "invitee-2" {
		t.Fatalf("revoked fanout = %+v, want invitee-2 only", fb.userCalls)
	}
}

func inviteePtr(s string) *string { return &s }

// IM outbounds (Slack, Lark, ...) subscribe to chat:done and task:message on
// the bus in-process. Narrowing the WebSocket fanout must not narrow them.
func TestRegisterListeners_ChatRoutingKeepsInProcessListeners(t *testing.T) {
	bus := events.New()
	fb := &fakeBroadcaster{}
	var got []events.Event
	for _, eventType := range []string{protocol.EventChatDone, protocol.EventTaskMessage} {
		bus.Subscribe(eventType, func(e events.Event) { got = append(got, e) })
	}
	registerListeners(bus, fb)

	done := protocol.ChatDonePayload{ChatSessionID: "chat-1", TaskID: "task-1", Content: "assistant reply"}
	message := protocol.TaskMessagePayload{TaskID: "task-1", Seq: 1, Type: "text", Content: "streamed text"}
	for _, e := range []events.Event{
		{Type: protocol.EventChatDone, WorkspaceID: "ws-1", ChatSessionID: "chat-1", ChatSessionOwnerID: "owner-1", Payload: done},
		{Type: protocol.EventTaskMessage, WorkspaceID: "ws-1", TaskID: "task-1", ChatSessionID: "chat-1", ChatSessionOwnerID: "owner-1", Payload: message},
		// Ownerless events are dropped from the WebSocket only.
		{Type: protocol.EventChatDone, WorkspaceID: "ws-1", ChatSessionID: "chat-1", Payload: done},
	} {
		bus.Publish(e)
	}

	if len(got) != 3 {
		t.Fatalf("in-process listener saw %d events, want 3", len(got))
	}
	if p, ok := got[0].Payload.(protocol.ChatDonePayload); !ok || p.Content != "assistant reply" {
		t.Fatalf("chat:done payload = %#v, want the full reply", got[0].Payload)
	}
	if p, ok := got[1].Payload.(protocol.TaskMessagePayload); !ok || p.Content != "streamed text" {
		t.Fatalf("task:message payload = %#v, want the full message", got[1].Payload)
	}
	if p, ok := got[2].Payload.(protocol.ChatDonePayload); !ok || p.Content != "assistant reply" {
		t.Fatalf("ownerless chat:done payload = %#v, want the full reply", got[2].Payload)
	}
}

// A pin is one member's own in every workspace, with or without a private
// project: one published without its member reaches nobody.
func TestRegisterListeners_PinEventsWithoutMemberAreDropped(t *testing.T) {
	for _, eventType := range []string{protocol.EventPinCreated, protocol.EventPinDeleted, protocol.EventPinReordered} {
		t.Run(eventType, func(t *testing.T) {
			bus := events.New()
			fb := &fakeBroadcaster{}
			registerListeners(bus, fb)

			bus.Publish(events.Event{
				Type: eventType, WorkspaceID: "ws-1", ActorType: "member", ActorID: "owner-1",
				Payload: map[string]any{"item_type": "issue", "item_id": "issue-1"},
			})

			fb.mu.Lock()
			defer fb.mu.Unlock()
			if len(fb.workspaceCalls) != 0 || len(fb.scopeCalls) != 0 || len(fb.userCalls) != 0 || fb.broadcastCalled != 0 {
				t.Fatalf("pin event was delivered: workspace=%+v scope=%+v user=%+v broadcast=%d",
					fb.workspaceCalls, fb.scopeCalls, fb.userCalls, fb.broadcastCalled)
			}
		})
	}
}

func TestRegisterListeners_MemberPrivateEventsGoOnlyToThatMember(t *testing.T) {
	for _, eventType := range []string{
		protocol.EventIssueViewPreferenceChanged, protocol.EventIssueViewChanged,
		protocol.EventPinCreated, protocol.EventPinDeleted, protocol.EventPinReordered,
	} {
		t.Run(eventType, func(t *testing.T) {
			bus := events.New()
			fb := &fakeBroadcaster{}
			registerListeners(bus, fb)

			bus.Publish(events.Event{
				Type: eventType, WorkspaceID: "ws-1", ActorType: "member", ActorID: "owner-1",
				MemberPrivateUserID: "owner-1",
				Payload:             map[string]any{"view_id": "view-1"},
			})

			msg := onlyOwnerWorkspaceFrame(t, fb, "ws-1", "owner-1")
			var frame struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal(msg, &frame); err != nil || frame.Type != eventType {
				t.Fatalf("member frame type = %q (%v), want %q", frame.Type, err, eventType)
			}
		})
	}
}

// Preferences have no audience but their owner: without one, nobody hears it.
func TestRegisterListeners_PreferenceEventWithoutMemberIsDropped(t *testing.T) {
	bus := events.New()
	fb := &fakeBroadcaster{}
	registerListeners(bus, fb)

	bus.Publish(events.Event{
		Type: protocol.EventIssueViewPreferenceChanged, WorkspaceID: "ws-1", ActorType: "member", ActorID: "owner-1",
		Payload: map[string]any{"scope_type": "sidebar_projects"},
	})

	if len(fb.workspaceCalls) != 0 || len(fb.userCalls) != 0 || len(fb.scopeCalls) != 0 || fb.broadcastCalled != 0 {
		t.Fatalf("preference event without a member was delivered: workspace=%+v user=%+v scope=%+v broadcast=%d",
			fb.workspaceCalls, fb.userCalls, fb.scopeCalls, fb.broadcastCalled)
	}
}

// A shared view's change is news to the whole workspace.
func TestRegisterListeners_SharedIssueViewChangeGoesToWorkspace(t *testing.T) {
	bus := events.New()
	fb := &fakeBroadcaster{}
	registerListeners(bus, fb)

	bus.Publish(events.Event{
		Type: protocol.EventIssueViewChanged, WorkspaceID: "ws-1", ActorType: "member", ActorID: "owner-1",
		Payload: map[string]any{"view_id": "view-1", "action": "updated"},
	})

	if len(fb.scopeCalls) != 0 || len(fb.workspaceCalls) != 1 || fb.workspaceCalls[0].workspaceID != "ws-1" {
		t.Fatalf("shared view change fanout: scope=%+v workspace=%+v, want ws-1 once", fb.scopeCalls, fb.workspaceCalls)
	}
}

// An invitation conclusion must reach the invitee BOTH ways: the workspace
// broadcast keeps admin pending lists fresh, and a targeted send reaches the
// invitee's own clients — which are usually bound to a different workspace
// room (or no room at all, #8432), so the broadcast alone never arrives.
// The targeted frame passes excludeWorkspace so clients already in the room
// don't get the event twice.
