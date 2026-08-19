package daemonws

import (
	"log/slog"

	"github.com/oklog/ulid/v2"

	"github.com/multica-ai/multica/server/internal/realtime"
)

// RelayNotifier sends daemon wakeup hints to the local daemon hub and, when
// Redis is configured, publishes the same hint through the shared realtime
// relay so every API node can attempt local delivery.
type RelayNotifier struct {
	local *Hub
	relay realtime.RelayPublisher
	// wakeupsViaRelay gates the per-task/runtime wakeup publishes. The
	// legacy relay only consumes the fixed daemon control scope, so wakeup
	// publishes there would accumulate one unbounded, never-consumed stream
	// key per task — legacy mode keeps wakeups local-only (its original
	// behavior) and rides the relay for control frames alone.
	wakeupsViaRelay bool
}

func NewRelayNotifier(local *Hub, relay realtime.RelayPublisher) *RelayNotifier {
	return &RelayNotifier{local: local, relay: relay, wakeupsViaRelay: true}
}

// NewControlOnlyRelayNotifier is the legacy-relay-mode variant: wakeup hints
// stay node-local, only daemon control frames (runtime revocations) publish
// through the relay.
func NewControlOnlyRelayNotifier(local *Hub, relay realtime.RelayPublisher) *RelayNotifier {
	return &RelayNotifier{local: local, relay: relay, wakeupsViaRelay: false}
}

func (n *RelayNotifier) NotifyTaskAvailable(runtimeID, taskID string) {
	if runtimeID == "" {
		return
	}
	eventID := ulid.Make().String()
	if n.local != nil {
		n.local.notifyTaskAvailable(runtimeID, taskID, eventID)
	}
	if n.relay == nil || !n.wakeupsViaRelay {
		return
	}
	frame, err := taskAvailableFrame(runtimeID, taskID)
	if err != nil {
		M.WakeupPublishErrors.Add(1)
		return
	}
	shardKey := taskID
	if shardKey == "" {
		shardKey = eventID
	}
	if err := n.relay.PublishWithID(realtime.ScopeDaemonRuntime, shardKey, "", frame, eventID); err != nil {
		M.WakeupPublishErrors.Add(1)
		slog.Warn("daemon websocket wakeup publish failed", "error", err, "runtime_id", runtimeID, "task_id", taskID)
		return
	}
	M.WakeupPublishedTotal.Add(1)
}

func (n *RelayNotifier) NotifyRuntimeProfilesChanged(workspaceID, profileID string) {
	if workspaceID == "" {
		return
	}
	eventID := ulid.Make().String()
	if n.local != nil {
		n.local.notifyRuntimeProfilesChanged(workspaceID, profileID, eventID)
	}
	if n.relay == nil || !n.wakeupsViaRelay {
		return
	}
	frame, err := runtimeProfilesChangedFrame(workspaceID, profileID)
	if err != nil {
		M.WakeupPublishErrors.Add(1)
		return
	}
	if err := n.relay.PublishWithID(realtime.ScopeDaemonRuntime, workspaceID, "", frame, eventID); err != nil {
		M.WakeupPublishErrors.Add(1)
		slog.Warn("daemon websocket profile refresh publish failed", "error", err, "workspace_id", workspaceID, "runtime_profile_id", profileID)
		return
	}
	M.WakeupPublishedTotal.Add(1)
}

func (n *RelayNotifier) NotifyWorkspacesChanged(userID string) {
	if userID == "" {
		return
	}
	eventID := ulid.Make().String()
	if n.local != nil {
		n.local.notifyWorkspacesChanged(userID, eventID)
	}
	if n.relay == nil || !n.wakeupsViaRelay {
		return
	}
	frame, err := workspacesChangedFrame()
	if err != nil {
		M.WakeupPublishErrors.Add(1)
		return
	}
	// ScopeDaemonRuntime is the relay's daemon-only transport scope; the frame
	// type tells Hub.DeliverDaemonRuntime whether scopeID is a runtime,
	// workspace, or user key. Keeping one transport scope preserves compatibility
	// with existing relay consumers while the hub enforces user-scoped delivery.
	if err := n.relay.PublishWithID(realtime.ScopeDaemonRuntime, userID, "", frame, eventID); err != nil {
		M.WakeupPublishErrors.Add(1)
		slog.Warn("daemon websocket workspace refresh publish failed", "error", err, "user_id", userID)
		return
	}
	M.WakeupPublishedTotal.Add(1)
}

// NotifyPendingWork fans a runtime-scoped "heartbeat now" hint out to the local
// hub and, when Redis is configured, through the relay so the API node that
// actually holds the daemon's WebSocket delivers it (MUL-5444). Shard key is the
// runtime ID: hints for one runtime stay ordered relative to each other, and a
// dropped hint only costs the daemon its normal heartbeat delay.
func (n *RelayNotifier) NotifyPendingWork(runtimeID, kind string) {
	if runtimeID == "" {
		return
	}
	eventID := ulid.Make().String()
	if n.local != nil {
		n.local.notifyPendingWork(runtimeID, kind, eventID)
	}
	if n.relay == nil || !n.wakeupsViaRelay {
		return
	}
	frame, err := pendingWorkFrame(runtimeID, kind)
	if err != nil {
		M.WakeupPublishErrors.Add(1)
		return
	}
	if err := n.relay.PublishWithID(realtime.ScopeDaemonRuntime, runtimeID, "", frame, eventID); err != nil {
		M.WakeupPublishErrors.Add(1)
		slog.Warn("daemon websocket pending work publish failed", "error", err, "runtime_id", runtimeID, "kind", kind)
		return
	}
	M.WakeupPublishedTotal.Add(1)
}

// DisconnectRuntimes severs the daemon WebSockets for runtimeIDs on this node
// and, when Redis is configured, publishes the revocation through the relay so
// every other API node severs its own sockets too — an account suspension must
// not depend on which node happens to hold the daemon's connection. The
// signature mirrors Hub.DisconnectRuntimes so either can back
// Handler.DisconnectDaemonRuntimes.
func (n *RelayNotifier) DisconnectRuntimes(runtimeIDs []string) error {
	if len(runtimeIDs) == 0 {
		return nil
	}
	if n.local != nil {
		n.local.DisconnectRuntimes(runtimeIDs)
	}
	if n.relay == nil {
		return nil
	}
	eventID := ulid.Make().String()
	frame, err := runtimesRevokedFrame(runtimeIDs)
	if err != nil {
		M.WakeupPublishErrors.Add(1)
		return err
	}
	// Unlike the wakeup hints above, a lost revocation is NOT harmless — a
	// daemon socket on another node would keep serving a suspended user — so
	// the publish error is returned for the suspend endpoint to surface, and
	// the frame rides the FIXED control scope that every relay with a daemon
	// deliverer consumes unconditionally (per-runtime shard streams have no
	// consumer in legacy relay mode).
	if err := n.relay.PublishWithID(realtime.ScopeDaemonRuntime, realtime.DaemonControlScopeID, "", frame, eventID); err != nil {
		M.WakeupPublishErrors.Add(1)
		slog.Warn("daemon websocket revoke publish failed", "error", err, "runtime_ids", runtimeIDs)
		return err
	}
	M.WakeupPublishedTotal.Add(1)
	return nil
}
