package main

import (
	"sync"
	"testing"

	"github.com/multica-ai/multica/server/internal/events"
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
				Payload:     map[string]any{"task_id": tc.taskID},
			})

			if len(fb.scopeCalls) != 1 {
				t.Fatalf("BroadcastToScope calls = %d, want 1", len(fb.scopeCalls))
			}
			if fb.scopeCalls[0].scopeType != "task" || fb.scopeCalls[0].scopeID != tc.taskID {
				t.Fatalf("task event scope = %s:%s, want task:%s", fb.scopeCalls[0].scopeType, fb.scopeCalls[0].scopeID, tc.taskID)
			}
			if len(fb.workspaceCalls) != 0 {
				t.Fatalf("task content leaked to %d workspace broadcast(s)", len(fb.workspaceCalls))
			}
		})
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
		RecipientUserID: "creator-1",
		Payload:         map[string]any{"chat_session_id": "chat-1", "content": "private reply"},
	})

	if len(fb.userCalls) != 1 || fb.userCalls[0].userID != "creator-1" {
		t.Fatalf("chat event recipient calls = %+v, want creator-1", fb.userCalls)
	}
	if len(fb.workspaceCalls) != 0 || len(fb.scopeCalls) != 0 {
		t.Fatalf("chat content escaped creator routing: workspaces=%+v scopes=%+v", fb.workspaceCalls, fb.scopeCalls)
	}
}

func TestRegisterListeners_ChatContentWithoutTrustedRecipientDrops(t *testing.T) {
	bus := events.New()
	fb := &fakeBroadcaster{}
	registerListeners(bus, fb)

	bus.Publish(events.Event{
		Type:        protocol.EventChatMessage,
		WorkspaceID: "ws-1",
		Payload:     map[string]any{"chat_session_id": "chat-1", "content": "private prompt"},
	})

	if len(fb.userCalls) != 0 || len(fb.workspaceCalls) != 0 || len(fb.scopeCalls) != 0 {
		t.Fatalf("chat content without trusted recipient was delivered: users=%+v workspaces=%+v scopes=%+v", fb.userCalls, fb.workspaceCalls, fb.scopeCalls)
	}
}
