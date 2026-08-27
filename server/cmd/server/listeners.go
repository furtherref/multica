package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/handler"
	"github.com/multica-ai/multica/server/internal/realtime"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// internalOnlyPayloadKeys lists payload keys that exist purely for in-process
// listeners and must never be serialized to a WebSocket client.
//
// `issue:updated` carries prev_description and prev_title so the in-process
// listeners can diff against the new values: subscriber_listeners.go adds newly
// @mentioned users, notification_listeners.go builds mention notifications, and
// activity_listeners.go records the title change. Those all run on
// bus.Subscribe, which Publish dispatches BEFORE the SubscribeAll forwarder
// below, so removing the keys on the way out cannot affect them.
//
// No client reads either key — IssueUpdatedPayload in
// packages/core/types/events.ts does not declare them. They reached the wire
// only because the forwarder reuses the producer's payload map verbatim, which
// meant every description autosave broadcast TWO full copies of the description
// (the new one inside `issue`, plus prev_description) to every connection in the
// workspace, including users who did not have the issue open. The DB write is
// O(1); the fanout was O(workspace connections × description size) (MUL-5492).
//
// This is a table rather than an `if` on one event type because the bug was
// structural, not a typo: the next large field added to a published payload
// inherits the same cost silently. Keeping the list declarative puts the
// internal/external payload boundary in one reviewable place.
var internalOnlyPayloadKeys = map[string][]string{
	protocol.EventIssueUpdated: {"prev_description", "prev_title"},
}

// publicRealtimePayloadKeys is the external contract registry for task and
// Chat events. These event families routinely carry routing metadata and
// in-process fields that must not reach a browser. A positive allowlist keeps a
// newly-added producer field private until the external contract is reviewed.
var publicRealtimePayloadKeys = map[string][]string{
	protocol.EventTaskQueued:                {"task_id", "issue_id", "status", "chat_session_id"},
	protocol.EventTaskDispatch:              {"task_id", "issue_id", "runtime_id", "chat_session_id"},
	protocol.EventTaskRunning:               {"task_id", "issue_id", "status", "chat_session_id"},
	protocol.EventTaskWaitingLocalDirectory: {"task_id", "issue_id", "status", "chat_session_id", "wait_reason"},
	protocol.EventTaskProgress:              {"task_id", "summary", "step", "total"},
	protocol.EventTaskCompleted:             {"task_id", "issue_id", "status", "chat_session_id"},
	protocol.EventTaskFailed:                {"task_id", "issue_id", "status", "chat_session_id"},
	protocol.EventTaskMessage:               {"task_id", "issue_id", "seq", "type", "tool", "content", "input", "output", "created_at"},
	protocol.EventTaskActivity:              {"task_id", "issue_id", "activity", "after_seq"},
	protocol.EventTaskCancelled:             {"task_id", "issue_id", "status", "chat_session_id"},

	protocol.EventChatMessage:         {"chat_session_id", "message_id", "role", "content", "task_id", "created_at"},
	protocol.EventChatDone:            {"chat_session_id", "task_id", "message_id", "content", "elapsed_ms", "created_at", "message_kind", "quick_actions", "quick_actions_pending"},
	protocol.EventChatQuickActions:    {"chat_session_id", "task_id", "message_id", "quick_actions", "failed"},
	protocol.EventChatCancelFinalized: {"outcome", "chat_session_id", "task_id", "initiator_user_id", "message_id", "content", "message_kind", "created_at", "elapsed_ms"},
	protocol.EventChatSessionRead:     {"chat_session_id"},
	protocol.EventChatSessionDeleted:  {"chat_session_id"},
	protocol.EventChatSessionUpdated:  {"chat_session_id", "title", "project_id", "pinned", "status", "updated_at"},
}

func isContractProtectedRealtimeEvent(eventType string) bool {
	return strings.HasPrefix(eventType, "task:") || strings.HasPrefix(eventType, "chat:")
}

func isTaskScopedRealtimeEvent(eventType string) bool {
	switch eventType {
	case protocol.EventTaskMessage, protocol.EventTaskProgress, protocol.EventTaskActivity:
		return true
	default:
		return false
	}
}

func isTaskLifecycleRealtimeEvent(eventType string) bool {
	switch eventType {
	case protocol.EventTaskQueued,
		protocol.EventTaskDispatch,
		protocol.EventTaskRunning,
		protocol.EventTaskWaitingLocalDirectory,
		protocol.EventTaskCompleted,
		protocol.EventTaskFailed,
		protocol.EventTaskCancelled:
		return true
	default:
		return false
	}
}

func payloadMap(payload any) (map[string]any, bool) {
	if m, ok := payload.(map[string]any); ok {
		return m, true
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, false
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil || m == nil {
		return nil, false
	}
	return m, true
}

// projectOutbound returns payload with the event type's internal-only keys
// removed, ready to serialize for external consumers.
//
// The input map is never mutated. In-process listeners have already run by the
// time this is called, but the producer still owns the map and a second
// forwarder may yet read it, so mutating it in place would be a landmine.
func projectOutbound(eventType string, payload any) any {
	if publicKeys, registered := publicRealtimePayloadKeys[eventType]; registered {
		m, ok := payloadMap(payload)
		if !ok {
			return nil
		}
		projected := make(map[string]any, len(publicKeys))
		for _, key := range publicKeys {
			if value, present := m[key]; present {
				projected[key] = value
			}
		}
		return projected
	}
	if isContractProtectedRealtimeEvent(eventType) {
		return nil
	}

	keys := internalOnlyPayloadKeys[eventType]
	if len(keys) == 0 {
		return payload
	}
	m, ok := payload.(map[string]any)
	if !ok {
		return payload
	}
	projected := make(map[string]any, len(m))
	for k, v := range m {
		projected[k] = v
	}
	for _, k := range keys {
		delete(projected, k)
	}
	return projected
}

// registerListeners wires up event bus listeners for WS broadcasting.
// Personal events (inbox, invites) are sent only to the target user via
// SendToUser. All other events are broadcast to the workspace room.
//
// The broadcaster parameter is intentionally typed as the realtime.Broadcaster
// interface (not *realtime.Hub) so that this layer can later be swapped out
// for a Redis-backed relay or a feature-flagged dual-write implementation
// without touching any of the event listeners below. This is Phase 0 of the
// horizontal-scaling plan tracked in MUL-1138.
func registerListeners(bus *events.Bus, b realtime.Broadcaster) {
	// Connection authorization is immutable. Mutations that can change an
	// actor's visible-Agent set close workspace connections on every node via
	// the relay-backed internal authorization scope; reconnect resolves a fresh
	// set before registration.
	invalidateWorkspaceAuthorization := func(e events.Event) {
		if e.WorkspaceID == "" {
			return
		}
		b.BroadcastToScope(realtime.ScopeWorkspaceAuthorization, e.WorkspaceID, realtime.AuthorizationChangedFrame())
	}
	for _, eventType := range []string{
		protocol.EventAgentCreated,
		protocol.EventAgentArchived,
		protocol.EventAgentRestored,
		protocol.EventMemberUpdated,
		protocol.EventMemberRemoved,
	} {
		bus.Subscribe(eventType, invalidateWorkspaceAuthorization)
	}
	bus.Subscribe(protocol.EventAgentStatus, func(e events.Event) {
		if e.AuthorizationChanged {
			invalidateWorkspaceAuthorization(e)
		}
	})

	// Personal events should NOT be broadcast to the whole workspace.
	personalEvents := map[string]bool{
		protocol.EventInboxNew:           true,
		protocol.EventInboxRead:          true,
		protocol.EventInboxArchived:      true,
		protocol.EventInboxUnarchived:    true,
		protocol.EventInboxBatchRead:     true,
		protocol.EventInboxBatchArchived: true,
		protocol.EventInvitationCreated:  true,
		protocol.EventInvitationRevoked:  true,
	}

	// Helper: marshal event and send to a specific user.
	sendToRecipient := func(b realtime.Broadcaster, e events.Event, recipientID string) {
		if recipientID == "" {
			return
		}
		data, err := json.Marshal(map[string]any{"type": e.Type, "payload": projectOutbound(e.Type, e.Payload), "actor_id": e.ActorID, "actor_type": e.ActorType})
		if err != nil {
			return
		}
		realtime.M.RecordEvent(e.Type)
		b.SendToUser(recipientID, data)
	}

	// inbox:new — extract recipient from nested item
	bus.Subscribe(protocol.EventInboxNew, func(e events.Event) {
		payload, ok := e.Payload.(map[string]any)
		if !ok {
			return
		}
		item, ok := payload["item"].(map[string]any)
		if !ok {
			return
		}
		recipientID, _ := item["recipient_id"].(string)
		sendToRecipient(b, e, recipientID)
	})

	// inbox:read, inbox:archived, inbox:unarchived, inbox:batch-read,
	// inbox:batch-archived — extract recipient from top-level payload
	for _, eventType := range []string{
		protocol.EventInboxRead, protocol.EventInboxArchived, protocol.EventInboxUnarchived,
		protocol.EventInboxBatchRead, protocol.EventInboxBatchArchived,
	} {
		bus.Subscribe(eventType, func(e events.Event) {
			payload, ok := e.Payload.(map[string]any)
			if !ok {
				return
			}
			recipientID, _ := payload["recipient_id"].(string)
			sendToRecipient(b, e, recipientID)
		})
	}

	// invitation:created — send to the invitee so they see the invitation in real time.
	bus.Subscribe(protocol.EventInvitationCreated, func(e events.Event) {
		payload, ok := e.Payload.(map[string]any)
		if !ok {
			return
		}
		inv, ok := payload["invitation"].(handler.InvitationResponse)
		if !ok {
			// Fallback for map encoding.
			if invMap, ok := payload["invitation"].(map[string]any); ok {
				if uid, _ := invMap["invitee_user_id"].(*string); uid != nil && *uid != "" {
					data, err := json.Marshal(map[string]any{"type": e.Type, "payload": projectOutbound(e.Type, e.Payload), "actor_id": e.ActorID, "actor_type": e.ActorType})
					if err != nil {
						return
					}
					realtime.M.RecordEvent(e.Type)
					b.SendToUser(*uid, data)
				}
			}
			return
		}
		if inv.InviteeUserID != nil && *inv.InviteeUserID != "" {
			data, err := json.Marshal(map[string]any{"type": e.Type, "payload": projectOutbound(e.Type, e.Payload), "actor_id": e.ActorID, "actor_type": e.ActorType})
			if err != nil {
				return
			}
			realtime.M.RecordEvent(e.Type)
			b.SendToUser(*inv.InviteeUserID, data)
		}
	})

	// invitation:revoked — send to the invitee so their pending list updates.
	bus.Subscribe(protocol.EventInvitationRevoked, func(e events.Event) {
		payload, ok := e.Payload.(map[string]any)
		if !ok {
			return
		}
		uid, _ := payload["invitee_user_id"].(*string)
		if uid != nil && *uid != "" {
			sendToRecipient(b, e, *uid)
		}
	})

	// member:added — also send to the invited user so they discover the new workspace.
	// Pass excludeWorkspace so clients already in the target room (reached via
	// BroadcastToWorkspace in SubscribeAll) don't receive the event twice.
	bus.Subscribe(protocol.EventMemberAdded, func(e events.Event) {
		payload, ok := e.Payload.(map[string]any)
		if !ok {
			return
		}
		var userID string
		switch m := payload["member"].(type) {
		case handler.MemberWithUserResponse:
			userID = m.UserID
		case map[string]any:
			userID, _ = m["user_id"].(string)
		default:
			slog.Warn("member:added: unexpected member payload type", "type", fmt.Sprintf("%T", payload["member"]))
		}
		if userID == "" {
			return
		}
		data, err := json.Marshal(map[string]any{"type": e.Type, "payload": projectOutbound(e.Type, e.Payload), "actor_id": e.ActorID, "actor_type": e.ActorType})
		if err != nil {
			return
		}
		realtime.M.RecordEvent(e.Type)
		b.SendToUser(userID, data, e.WorkspaceID)
	})

	// SubscribeAll handles workspace-broadcast for non-personal events.
	bus.SubscribeAll(func(e events.Event) {
		// Skip personal events — they are handled by type-specific listeners above.
		if personalEvents[e.Type] {
			return
		}

		payload := projectOutbound(e.Type, e.Payload)
		if payload == nil && isContractProtectedRealtimeEvent(e.Type) {
			realtime.M.MessagesDroppedTotal.Add(1)
			slog.Warn("dropping realtime event without a valid public contract", "event_type", e.Type)
			return
		}
		msg := map[string]any{
			"type":       e.Type,
			"payload":    payload,
			"actor_id":   e.ActorID,
			"actor_type": e.ActorType,
		}
		data, err := json.Marshal(msg)
		if err != nil {
			slog.Error("failed to marshal event", "event_type", e.Type, "error", err)
			return
		}

		if isTaskScopedRealtimeEvent(e.Type) {
			if e.TaskID == "" {
				realtime.M.MessagesDroppedTotal.Add(1)
				slog.Warn("dropping task-scoped realtime event without trusted task id", "event_type", e.Type)
				return
			}
			realtime.M.RecordEvent(e.Type)
			b.BroadcastToScope(realtime.ScopeTask, e.TaskID, data)
			return
		}

		if isTaskLifecycleRealtimeEvent(e.Type) {
			if e.AgentID == "" {
				realtime.M.MessagesDroppedTotal.Add(1)
				slog.Warn("dropping task lifecycle event without trusted Agent id", "event_type", e.Type)
				return
			}
			if e.ChatSessionID != "" {
				if e.RecipientUserID == "" {
					realtime.M.MessagesDroppedTotal.Add(1)
					slog.Warn("dropping creator-owned task lifecycle event without trusted recipient", "event_type", e.Type)
					return
				}
				realtime.M.RecordEvent(e.Type)
				b.BroadcastToScope(realtime.ScopeUserAgent, realtime.UserAgentScopeID(e.RecipientUserID, e.AgentID), data)
				return
			}
			if e.WorkspaceID == "" {
				realtime.M.MessagesDroppedTotal.Add(1)
				slog.Warn("dropping task lifecycle event without trusted workspace id", "event_type", e.Type)
				return
			}
			realtime.M.RecordEvent(e.Type)
			b.BroadcastToScope(realtime.ScopeWorkspaceAgent, realtime.WorkspaceAgentScopeID(e.WorkspaceID, e.AgentID), data)
			return
		}

		if strings.HasPrefix(e.Type, "chat:") {
			if e.RecipientUserID == "" || e.AgentID == "" {
				realtime.M.MessagesDroppedTotal.Add(1)
				slog.Warn("dropping creator-owned chat event without trusted authorization metadata", "event_type", e.Type)
				return
			}
			realtime.M.RecordEvent(e.Type)
			b.BroadcastToScope(realtime.ScopeUserAgent, realtime.UserAgentScopeID(e.RecipientUserID, e.AgentID), data)
			return
		}

		// Every recognized task event has returned through an authorized scope.
		// Unknown task contracts are dropped by projectOutbound above.

		if e.WorkspaceID != "" {
			realtime.M.RecordEvent(e.Type)
			b.BroadcastToWorkspace(e.WorkspaceID, data)
		} else if strings.HasPrefix(e.Type, "daemon:") {
			realtime.M.RecordEvent(e.Type)
			b.Broadcast(data)
		}
		// Otherwise drop — no global broadcast for non-daemon events without a workspace.
	})
}
