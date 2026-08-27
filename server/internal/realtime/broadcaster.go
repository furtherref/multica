package realtime

// Scope types recognised by the broadcaster. Producers and consumers should
// use these constants rather than raw strings so a typo can never silently
// route an event to a non-existent room.
const (
	ScopeWorkspace = "workspace"
	ScopeUser      = "user"
	ScopeTask      = "task"
	ScopeChat      = "chat"
	// ScopeWorkspaceAgent and ScopeUserAgent contain low-frequency task
	// lifecycle events. Connections join these rooms only for Agents visible
	// to the authenticated member at connection setup.
	ScopeWorkspaceAgent = "workspace_agent"
	ScopeUserAgent      = "user_agent"
	// Legacy Agent scopes are joined only by clients that predate explicit
	// task-scope subscriptions. They preserve installed-desktop transcript
	// streaming without reopening workspace-wide task-content fanout.
	ScopeLegacyWorkspaceAgent = "legacy_workspace_agent"
	ScopeLegacyUserAgent      = "legacy_user_agent"
	// ScopeWorkspaceAuthorization is an internal cross-node control scope.
	// Clients join it implicitly but frames are intercepted by the Hub and are
	// never delivered to their send channels.
	ScopeWorkspaceAuthorization = "workspace_authorization"
	// ScopeDaemonRuntime routes daemon wakeup frames through the Redis relay.
	// It is consumed by the daemon WebSocket hub, not by browser clients.
	ScopeDaemonRuntime = "daemon_runtime"
)

// WorkspaceAgentScopeID returns the immutable room key for an Agent visible
// within a workspace connection. Workspace and Agent IDs are UUIDs, so ':' is
// an unambiguous separator.
func WorkspaceAgentScopeID(workspaceID, agentID string) string {
	return workspaceID + ":" + agentID
}

// UserAgentScopeID returns the creator-specific Agent room key used by direct
// Chat task lifecycle events.
func UserAgentScopeID(userID, agentID string) string {
	return userID + ":" + agentID
}

// AuthorizationChangedFrame is the opaque relay payload for workspace
// authorization invalidation. Routing depends only on the scope, so the Hub
// never decodes this JSON on its hot path.
func AuthorizationChangedFrame() []byte {
	return []byte(`{"type":"authorization:changed"}`)
}

// AuthorizationExpandedFrame asks each node to resolve fresh Agent visibility
// and add only newly-visible rooms in place. It is safe only for mutations
// that cannot revoke access; narrowing mutations continue to disconnect.
func AuthorizationExpandedFrame() []byte {
	return []byte(`{"type":"authorization:expanded"}`)
}

// DaemonControlScopeID is the FIXED scope id daemon control frames (runtime
// revocations) publish under. Wakeup hints shard by task/runtime id, but the
// legacy relay only consumes streams the realtime hub has subscribers for —
// and nothing subscribes to daemon scopes — so control frames need one
// well-known stream every relay with a daemon deliverer consumes
// unconditionally.
const DaemonControlScopeID = "control"

// Broadcaster is the abstraction every realtime event producer should depend
// on instead of *Hub directly.
//
// Phase 1 (MUL-1138) extends the surface with BroadcastToScope so events can
// be fanned out to high-frequency per-resource scopes (`task:{id}`,
// `chat:{id}`) instead of the whole workspace. The legacy methods continue to
// work and now route through BroadcastToScope under the hood.
type Broadcaster interface {
	// BroadcastToScope fans a message out to every connection currently
	// subscribed to ({scopeType, scopeID}) on this node.
	BroadcastToScope(scopeType, scopeID string, message []byte)

	// BroadcastToWorkspace is a back-compat shortcut for
	// BroadcastToScope("workspace", workspaceID, message).
	BroadcastToWorkspace(workspaceID string, message []byte)

	// SendToUser is a back-compat shortcut for
	// BroadcastToScope("user", userID, message). The optional
	// excludeWorkspace argument is preserved for the `member:added`
	// dedup path: connections whose workspaceID matches excludeWorkspace
	// are skipped.
	SendToUser(userID string, message []byte, excludeWorkspace ...string)

	// Broadcast fans a message out to every connection on this node.
	// Used for daemon:* events that have no workspace scope.
	Broadcast(message []byte)
}

// DaemonRuntimeDeliverer consumes daemon-runtime scoped relay frames.
type DaemonRuntimeDeliverer interface {
	DeliverDaemonRuntime(scopeID string, frame []byte, eventID string)
}

// Compile-time assertion that *Hub continues to satisfy Broadcaster.
var _ Broadcaster = (*Hub)(nil)
