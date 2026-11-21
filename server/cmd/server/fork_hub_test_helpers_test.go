package main

import (
	"testing"

	"github.com/multica-ai/multica/server/internal/realtime"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// newAuthorizedTestHub builds a hub wired like main.go: the fork's realtime
// containment (#95) routes chat and task events through authorization-filtered
// Agent scopes, which a client joins only when the hub has the scope
// authorizer. Integration tests that read frames over a real socket need it.
func newAuthorizedTestHub(t *testing.T) *realtime.Hub {
	t.Helper()
	hub := realtime.NewHub()
	authorizer := newScopeAuthorizer(db.New(testPool))
	hub.SetAuthorizer(authorizer)
	hub.SetVisibleAgentResolver(authorizer)
	go hub.Run()
	return hub
}
