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
		ActorType: "member", ActorID: "creator-1", ChatSessionID: "chat-1",
		Payload: protocol.ChatSessionCreatedPayload{
			WorkspaceID: "ws-1", ChatSessionID: "chat-1", CreatorID: "creator-1",
			Title: "private opening title",
		},
	})

	if len(fb.workspaceCalls) != 0 {
		t.Fatalf("private Chat create reached workspace fanout: %+v", fb.workspaceCalls)
	}
	if len(fb.userCalls) != 1 || fb.userCalls[0].userID != "creator-1" {
		t.Fatalf("creator fanout = %+v, want creator-1 once", fb.userCalls)
	}
	var frame struct {
		Payload protocol.ChatSessionCreatedPayload `json:"payload"`
	}
	if err := json.Unmarshal(fb.userCalls[0].msg, &frame); err != nil {
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
		ActorType: "member", ActorID: "creator-1", ChatSessionID: "chat-1",
		Payload: protocol.ChatSessionUpdatedPayload{
			ChatSessionID: "chat-1", Title: "private derived title",
		},
	})

	if len(fb.workspaceCalls) != 0 {
		t.Fatalf("private Chat title reached workspace fanout: %+v", fb.workspaceCalls)
	}
	if len(fb.userCalls) != 1 || fb.userCalls[0].userID != "creator-1" {
		t.Fatalf("creator fanout = %+v, want creator-1 once", fb.userCalls)
	}
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
