package main

import (
	"context"
	"errors"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/handler"
	"github.com/multica-ai/multica/server/internal/realtime"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// scopeAuthQuerier is the narrow subset of db.Queries used by the scope
// authorizer. Declared as an interface so the authorizer can be unit tested
// with an in-memory fake (no DB required).
type scopeAuthQuerier interface {
	GetAgentTask(ctx context.Context, id pgtype.UUID) (db.AgentTaskQueue, error)
	GetIssue(ctx context.Context, id pgtype.UUID) (db.Issue, error)
	GetChatSession(ctx context.Context, id pgtype.UUID) (db.ChatSession, error)
	GetAgent(ctx context.Context, id pgtype.UUID) (db.Agent, error)
	GetMemberByUserAndWorkspace(ctx context.Context, arg db.GetMemberByUserAndWorkspaceParams) (db.Member, error)
	ListAgentInvocationTargets(ctx context.Context, agentID pgtype.UUID) ([]db.AgentInvocationTarget, error)
	ListSystemChatAgentIDsByCreator(ctx context.Context, arg db.ListSystemChatAgentIDsByCreatorParams) ([]pgtype.UUID, error)
}

// dbScopeAuthorizer implements realtime.ScopeAuthorizer for the per-task and
// per-chat scopes (workspace/user scopes are validated by the hub itself
// against the connection identity). It returns true only when the requested
// resource exists, belongs to the caller's workspace, and — for chat
// resources — was created by the caller (mirroring the HTTP creator-only
// access model).
type dbScopeAuthorizer struct{ q scopeAuthQuerier }

func newScopeAuthorizer(q scopeAuthQuerier) *dbScopeAuthorizer { return &dbScopeAuthorizer{q: q} }

// scopeLookupErr converts a scope-resource query error into an authorizer
// result. A missing resource (pgx.ErrNoRows) is a legitimate denial — the
// HTTP layer treats not-found as 404 rather than 403, so the realtime layer
// reports it as a plain "forbidden" refusal. Any other error (pool
// exhaustion, a cancelled context, a network blip) is a transient lookup
// failure and must propagate so handleSubscribe reports "lookup_failed"
// instead of masking a database outage as a wave of permission denials.
func scopeLookupErr(err error) (bool, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return false, err
}

func (a *dbScopeAuthorizer) AuthorizeScope(ctx context.Context, userID, workspaceID, scopeType, scopeID string) (bool, error) {
	if workspaceID == "" || scopeID == "" {
		return false, nil
	}
	wsUUID, err := util.ParseUUID(workspaceID)
	if err != nil {
		return false, nil
	}
	idUUID, err := util.ParseUUID(scopeID)
	if err != nil {
		return false, nil
	}
	switch scopeType {
	case realtime.ScopeTask:
		task, err := a.q.GetAgentTask(ctx, idUUID)
		if err != nil {
			return scopeLookupErr(err)
		}
		// Every task transcript inherits the linked Agent's REST visibility gate.
		// These lookups happen before Hub membership and never under the Hub lock.
		if task.IssueID.Valid {
			issue, err := a.q.GetIssue(ctx, task.IssueID)
			if err != nil {
				return scopeLookupErr(err)
			}
			if issue.WorkspaceID != wsUUID {
				return false, nil
			}
			return a.canViewTaskAgent(ctx, userID, wsUUID, task.AgentID)
		}
		// Chat tasks: only the chat session's creator may subscribe, mirroring
		// the HTTP layer's creator-only access on chat resources.
		if task.ChatSessionID.Valid {
			sess, err := a.q.GetChatSession(ctx, task.ChatSessionID)
			if err != nil {
				return scopeLookupErr(err)
			}
			if sess.WorkspaceID != wsUUID {
				return false, nil
			}
			if sess.AgentID != task.AgentID {
				return false, nil
			}
			uidUUID, err := util.ParseUUID(userID)
			if err != nil || sess.CreatorID != uidUUID {
				return false, nil
			}
			return a.canViewChatTaskAgent(ctx, userID, wsUUID, task.AgentID)
		}
		return false, nil
	case realtime.ScopeChat:
		sess, err := a.q.GetChatSession(ctx, idUUID)
		if err != nil {
			return scopeLookupErr(err)
		}
		if sess.WorkspaceID != wsUUID {
			return false, nil
		}
		// Chat sessions are private to their creator (see handler/chat.go:
		// GetChatSession / SendChatMessage / MarkChatSessionRead all enforce
		// CreatorID == userID). The realtime layer must not weaken this:
		// otherwise any workspace member who learns a session_id could
		// subscribe to chat:message / chat:done / chat:session_read for a
		// peer's private chat.
		uidUUID, err := util.ParseUUID(userID)
		if err != nil || sess.CreatorID != uidUUID {
			return false, nil
		}
		return true, nil
	default:
		return false, nil
	}
}

// VisibleAgentScopes returns the public REST-visible Agent population for
// workspace rooms, plus creator-owned system Chat carriers for private user
// rooms. Resolution happens once at connection setup, outside the Hub lock.
func (a *dbScopeAuthorizer) VisibleAgentScopes(ctx context.Context, userID, workspaceID string) (realtime.AgentScopeVisibility, error) {
	q, ok := a.q.(handler.AgentVisibilityQuerier)
	if !ok {
		return realtime.AgentScopeVisibility{}, errors.New("visible Agent query support is not configured")
	}
	wsUUID, err := util.ParseUUID(workspaceID)
	if err != nil {
		return realtime.AgentScopeVisibility{}, err
	}
	userUUID, err := util.ParseUUID(userID)
	if err != nil {
		return realtime.AgentScopeVisibility{}, err
	}
	member, err := a.q.GetMemberByUserAndWorkspace(ctx, db.GetMemberByUserAndWorkspaceParams{
		UserID: userUUID, WorkspaceID: wsUUID,
	})
	if err != nil {
		return realtime.AgentScopeVisibility{}, err
	}
	allowed, err := handler.ResolveMemberVisibleAgentIDs(ctx, q, wsUUID, userID, member.Role)
	if err != nil {
		return realtime.AgentScopeVisibility{}, err
	}
	workspaceVisible := make([]string, 0, len(allowed))
	for id := range allowed {
		workspaceVisible = append(workspaceVisible, id)
	}
	sort.Strings(workspaceVisible)

	userVisibleSet := make(map[string]struct{}, len(allowed))
	for id := range allowed {
		userVisibleSet[id] = struct{}{}
	}
	systemChatAgents, err := a.q.ListSystemChatAgentIDsByCreator(ctx, db.ListSystemChatAgentIDsByCreatorParams{
		WorkspaceID: wsUUID,
		CreatorID:   userUUID,
	})
	if err != nil {
		return realtime.AgentScopeVisibility{}, err
	}
	for _, id := range systemChatAgents {
		if id.Valid {
			userVisibleSet[util.UUIDToString(id)] = struct{}{}
		}
	}
	userVisible := make([]string, 0, len(userVisibleSet))
	for id := range userVisibleSet {
		userVisible = append(userVisible, id)
	}
	sort.Strings(userVisible)
	return realtime.AgentScopeVisibility{
		WorkspaceAgentIDs: workspaceVisible,
		UserAgentIDs:      userVisible,
	}, nil
}

func (a *dbScopeAuthorizer) canViewChatTaskAgent(ctx context.Context, userID string, workspaceID, agentID pgtype.UUID) (bool, error) {
	agent, err := a.q.GetAgent(ctx, agentID)
	if err != nil {
		return scopeLookupErr(err)
	}
	if agent.WorkspaceID != workspaceID {
		return false, nil
	}
	if agent.Kind == "system" {
		return true, nil
	}
	return a.canViewTaskAgent(ctx, userID, workspaceID, agentID)
}

func (a *dbScopeAuthorizer) canViewTaskAgent(ctx context.Context, userID string, workspaceID, agentID pgtype.UUID) (bool, error) {
	if !agentID.Valid {
		return false, nil
	}
	userUUID, err := util.ParseUUID(userID)
	if err != nil {
		return false, nil
	}
	agent, err := a.q.GetAgent(ctx, agentID)
	if err != nil {
		return scopeLookupErr(err)
	}
	if agent.WorkspaceID != workspaceID {
		return false, nil
	}
	member, err := a.q.GetMemberByUserAndWorkspace(ctx, db.GetMemberByUserAndWorkspaceParams{
		UserID: userUUID, WorkspaceID: workspaceID,
	})
	if err != nil {
		return scopeLookupErr(err)
	}
	targets, err := a.q.ListAgentInvocationTargets(ctx, agentID)
	if err != nil {
		return false, err
	}
	return handler.MemberAllowedToViewAgent(agent, targets, userID, member.Role), nil
}
