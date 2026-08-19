package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/auth"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// isSystemAdmin reports whether email belongs to the ADMIN_EMAILS allowlist.
// An empty allowlist means the deployment has no system admins — the admin
// API is entirely inert by default.
func (h *Handler) isSystemAdmin(email string) bool {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return false
	}
	for _, a := range h.cfg.AdminEmails {
		if strings.ToLower(strings.TrimSpace(a)) == email {
			return true
		}
	}
	return false
}

// requireSystemAdmin resolves the authenticated user and gates on the
// allowlist. Identity comes from the user row (by X-User-ID), never from
// X-User-Email — only the JWT auth path sets the email header.
func (h *Handler) requireSystemAdmin(w http.ResponseWriter, r *http.Request) (db.User, bool) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return db.User{}, false
	}
	user, err := h.Queries.GetUser(r.Context(), parseUUID(userID))
	if err != nil {
		writeError(w, http.StatusForbidden, "forbidden")
		return db.User{}, false
	}
	if !h.isSystemAdmin(user.Email) {
		writeError(w, http.StatusForbidden, "forbidden")
		return db.User{}, false
	}
	return user, true
}

type AdminUserResponse struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	Email         string  `json:"email"`
	AvatarURL     *string `json:"avatar_url"`
	AccountStatus string  `json:"account_status"`
	CreatedAt     string  `json:"created_at"`
}

func (h *Handler) ListAllUsers(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireSystemAdmin(w, r); !ok {
		return
	}
	users, err := h.Queries.ListUsersWithStatus(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list users")
		return
	}
	resp := make([]AdminUserResponse, 0, len(users))
	for _, u := range users {
		resp = append(resp, AdminUserResponse{
			ID:            uuidToString(u.ID),
			Name:          u.Name,
			Email:         u.Email,
			AvatarURL:     h.resolveAvatarURLPtr(textToPtr(u.AvatarUrl)),
			AccountStatus: u.AccountStatus,
			CreatedAt:     timestampToString(u.CreatedAt),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": resp})
}

type SetAccountStatusRequest struct {
	Status string `json:"status"`
}

// SetUserAccountStatus flips a user's account_status (active/suspended).
// Suspension is reversible admin action, not removal: unlike
// revokeAndRemoveMember, nothing here archives agents or deletes rows.
func (h *Handler) SetUserAccountStatus(w http.ResponseWriter, r *http.Request) {
	admin, ok := h.requireSystemAdmin(w, r)
	if !ok {
		return
	}
	targetID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	var req SetAccountStatusRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Status != auth.AccountStatusActive && req.Status != auth.AccountStatusSuspended {
		writeError(w, http.StatusBadRequest, "invalid status")
		return
	}
	if uuidToString(targetID) == uuidToString(admin.ID) {
		writeError(w, http.StatusForbidden, "cannot change your own account status")
		return
	}

	updated, err := h.Queries.SetUserAccountStatus(r.Context(), db.SetUserAccountStatusParams{
		ID:            targetID,
		AccountStatus: req.Status,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}

	targetIDStr := uuidToString(targetID)
	// Revocation must beat the cache TTL: without this the "next request
	// fails" promise silently becomes "fails within 10 minutes".
	if h.AccountGuard != nil {
		h.AccountGuard.Cache.Invalidate(r.Context(), targetIDStr)
	}

	if req.Status == auth.AccountStatusSuspended {
		h.quiesceSuspendedUser(r.Context(), targetID, targetIDStr)
	}

	writeJSON(w, http.StatusOK, AdminUserResponse{
		ID:            targetIDStr,
		Name:          updated.Name,
		Email:         updated.Email,
		AvatarURL:     h.resolveAvatarURLPtr(textToPtr(updated.AvatarUrl)),
		AccountStatus: updated.AccountStatus,
		CreatedAt:     timestampToString(updated.CreatedAt),
	})
}

// quiesceSuspendedUser converges runtime-side state after a suspension: any
// in-flight tasks on the user's runtimes are cancelled (so agents stop
// gracefully), those runtimes are forced offline, and any daemon_token rows
// for their daemons are deleted. Unlike member removal (revokeAndRemoveMember)
// nothing is archived — suspension is reversible: an unsuspended user simply
// re-pairs the daemon to get a fresh mdt_ token. Deleting the daemon token is
// what makes suspension actually stick for the mdt_ auth path — without it a
// suspended user's already-running daemon keeps daemon-API access (DaemonAuth
// has no user identity on that path) and its heartbeat/register calls bring
// force-offlined runtimes back online, undoing the rest of this convergence.
// Failures are logged, not surfaced: the status flip and cache invalidation in
// SetUserAccountStatus are the security boundary; this is best-effort cleanup.
func (h *Handler) quiesceSuspendedUser(ctx context.Context, userID pgtype.UUID, userIDStr string) {
	runtimes, err := h.Queries.ListAgentRuntimesByOwnerAllWorkspaces(ctx, userID)
	if err != nil {
		slog.Error("suspend: list runtimes failed", "user_id", userIDStr, "error", err)
	} else if len(runtimes) > 0 {
		runtimeIDs := make([]pgtype.UUID, len(runtimes))
		// A user's runtimes can span workspaces; AgentTaskQueue carries no
		// workspace_id of its own, so resolve each cancelled task's
		// workspace through the runtime it was queued on.
		workspaceByRuntime := make(map[string]string, len(runtimes))
		for i, rt := range runtimes {
			runtimeIDs[i] = rt.ID
			workspaceByRuntime[uuidToString(rt.ID)] = uuidToString(rt.WorkspaceID)
		}
		cancelled, err := h.Queries.CancelAgentTasksByRuntimeOrAgent(ctx, db.CancelAgentTasksByRuntimeOrAgentParams{
			RuntimeIds: runtimeIDs,
			AgentIds:   nil,
		})
		if err != nil {
			slog.Error("suspend: cancel tasks failed", "user_id", userIDStr, "error", err)
		} else if h.TaskService != nil {
			// Group per workspace: BroadcastCancelledTasks takes one workspace.
			byWs := map[string][]db.AgentTaskQueue{}
			for _, t := range cancelled {
				wsID := workspaceByRuntime[uuidToString(t.RuntimeID)]
				byWs[wsID] = append(byWs[wsID], t)
			}
			for wsID, tasks := range byWs {
				if wsID == "" {
					continue
				}
				h.TaskService.BroadcastCancelledTasks(ctx, wsID, tasks)
			}
		}
		if _, err := h.Queries.ForceOfflineRuntimesByIDs(ctx, runtimeIDs); err != nil {
			slog.Error("suspend: force offline failed", "user_id", userIDStr, "error", err)
		}

		// Delete daemon_token rows for these runtimes' daemons, mirroring
		// revokeAndRemoveMember (workspace_revoke.go). DeleteDaemonTokensByWorkspaceAndDaemons
		// is scoped to one workspace, and quiesce spans every workspace the
		// user has runtimes in, so group daemon IDs per workspace first.
		daemonIDsByWs := map[string][]string{}
		for _, rt := range runtimes {
			if rt.DaemonID.Valid && rt.DaemonID.String != "" {
				wsID := uuidToString(rt.WorkspaceID)
				daemonIDsByWs[wsID] = append(daemonIDsByWs[wsID], rt.DaemonID.String)
			}
		}
		for wsID, daemonIDs := range daemonIDsByWs {
			hashes, err := h.Queries.DeleteDaemonTokensByWorkspaceAndDaemons(ctx, db.DeleteDaemonTokensByWorkspaceAndDaemonsParams{
				WorkspaceID: parseUUID(wsID),
				DaemonIds:   daemonIDs,
			})
			if err != nil {
				slog.Error("suspend: delete daemon tokens failed", "user_id", userIDStr, "workspace_id", wsID, "error", err)
				continue
			}
			if h.DaemonTokenCache != nil {
				for _, hash := range hashes {
					h.DaemonTokenCache.Invalidate(ctx, hash)
				}
			}
		}
	}

	if h.DisconnectUser != nil {
		h.DisconnectUser(userIDStr)
	}
}
