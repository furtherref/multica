package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
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
	// ?status= narrows the listing by account_status. Default is "active" so
	// the console's landing view hides suspended accounts unless asked for.
	status := r.URL.Query().Get("status")
	if status == "" {
		status = auth.AccountStatusActive
	}
	switch status {
	case auth.AccountStatusActive, auth.AccountStatusSuspended, "all":
	default:
		writeError(w, http.StatusBadRequest, "invalid status")
		return
	}
	users, err := h.Queries.ListUsersWithStatus(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list users")
		return
	}
	resp := make([]AdminUserResponse, 0, len(users))
	for _, u := range users {
		if status != "all" && u.AccountStatus != status {
			continue
		}
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

	result, err := h.applyAccountStatusChange(r.Context(), targetID, req.Status)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		// The transaction rolled back: the status did NOT change, so 200
		// here would falsely report a suspension that never converged.
		slog.Error("account status: transaction failed",
			"target_id", uuidToString(targetID), "status", req.Status, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to update account status")
		return
	}

	targetIDStr := uuidToString(targetID)
	// The caches were already cleared pre-commit (see applyAccountStatusChange);
	// this second pass closes the small window where a concurrent request
	// re-cached a verdict between that invalidation and the commit. The
	// account entry is retried and surfaced — the PATCH is idempotent, so the
	// admin's retry re-runs it against the committed status. Daemon-token
	// entries are best-effort here: their hashes are unrecoverable after the
	// committed delete, they were already cleared pre-commit, and a re-cached
	// entry expires within AuthCacheTTL.
	if h.AccountGuard != nil {
		if err := h.AccountGuard.Cache.Invalidate(r.Context(), targetIDStr); err != nil {
			if err = h.AccountGuard.Cache.Invalidate(r.Context(), targetIDStr); err != nil {
				slog.Error("account status: post-commit cache revocation failed",
					"target_id", targetIDStr, "status", req.Status, "error", err)
				writeError(w, http.StatusInternalServerError, "account status updated but revocation propagation failed; retry the request")
				return
			}
		}
	}
	// Live-connection revocation must also hold before we report success: a
	// swallowed relay-publish failure would leave sockets on other nodes
	// serving a suspended user. The runtime IDs and user ID are re-derivable,
	// so the idempotent retry re-runs the kicks.
	if err := h.publishAccountStatusChange(r.Context(), targetIDStr, req.Status, result); err != nil {
		slog.Error("account status: live-connection revocation failed",
			"target_id", targetIDStr, "status", req.Status, "error", err)
		writeError(w, http.StatusInternalServerError, "account status updated but live-connection revocation failed; retry the request")
		return
	}

	writeJSON(w, http.StatusOK, AdminUserResponse{
		ID:            targetIDStr,
		Name:          result.Updated.Name,
		Email:         result.Updated.Email,
		AvatarURL:     h.resolveAvatarURLPtr(textToPtr(result.Updated.AvatarUrl)),
		AccountStatus: result.Updated.AccountStatus,
		CreatedAt:     timestampToString(result.Updated.CreatedAt),
	})
}

// accountStatusChangeResult carries everything the transaction touched so the
// post-commit side effects (cache invalidation, broadcasts, WS kicks) can run
// without re-querying — mirroring revokeAndRemoveMember's revocationResult.
type accountStatusChangeResult struct {
	Updated            db.User
	Cancelled          []db.AgentTaskQueue
	WorkspaceByRuntime map[string]string
	RuntimeIDStrs      []string
}

// applyAccountStatusChange flips account_status and, on suspension, converges
// all runtime-side DB state in the SAME transaction: cancel in-flight tasks on
// the user's runtimes, force those runtimes offline, and delete their daemons'
// mdt_ tokens. One transaction means a 200 response can never report a
// suspension whose convergence silently half-failed — either everything
// commits or the caller gets an error and nothing changed. Unlike member
// removal (revokeAndRemoveMember) nothing is archived — suspension is
// reversible: an unsuspended user simply re-pairs the daemon for a fresh
// mdt_ token. Deleting the daemon token is what makes suspension stick for
// the mdt_ auth path (DaemonAuth has no user identity there), and without it
// the daemon's heartbeat/register calls would bring force-offlined runtimes
// back online.
func (h *Handler) applyAccountStatusChange(ctx context.Context, userID pgtype.UUID, status string) (accountStatusChangeResult, error) {
	var empty accountStatusChangeResult

	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback(ctx)

	qtx := h.Queries.WithTx(tx)

	result := accountStatusChangeResult{}
	result.Updated, err = qtx.SetUserAccountStatus(ctx, db.SetUserAccountStatusParams{
		ID:            userID,
		AccountStatus: status,
	})
	if err != nil {
		return empty, err
	}

	if status == auth.AccountStatusSuspended {
		runtimes, err := qtx.ListAgentRuntimesByOwnerAllWorkspaces(ctx, userID)
		if err != nil {
			return empty, err
		}
		if len(runtimes) > 0 {
			runtimeIDs := make([]pgtype.UUID, len(runtimes))
			// A user's runtimes can span workspaces; AgentTaskQueue carries
			// no workspace_id of its own, so resolve each cancelled task's
			// workspace through the runtime it was queued on.
			result.WorkspaceByRuntime = make(map[string]string, len(runtimes))
			result.RuntimeIDStrs = make([]string, len(runtimes))
			for i, rt := range runtimes {
				runtimeIDs[i] = rt.ID
				result.RuntimeIDStrs[i] = uuidToString(rt.ID)
				result.WorkspaceByRuntime[uuidToString(rt.ID)] = uuidToString(rt.WorkspaceID)
			}
			result.Cancelled, err = qtx.CancelAgentTasksByRuntimeOrAgent(ctx, db.CancelAgentTasksByRuntimeOrAgentParams{
				RuntimeIds: runtimeIDs,
				AgentIds:   nil,
			})
			if err != nil {
				return empty, err
			}
			if _, err := qtx.ForceOfflineRuntimesByIDs(ctx, runtimeIDs); err != nil {
				return empty, err
			}

			// DeleteDaemonTokensByWorkspaceAndDaemons is scoped to one
			// workspace and this convergence spans every workspace the user
			// has runtimes in, so group daemon IDs per workspace first.
			daemonIDsByWs := map[string][]string{}
			for _, rt := range runtimes {
				if rt.DaemonID.Valid && rt.DaemonID.String != "" {
					wsID := uuidToString(rt.WorkspaceID)
					daemonIDsByWs[wsID] = append(daemonIDsByWs[wsID], rt.DaemonID.String)
				}
			}
			for wsID, daemonIDs := range daemonIDsByWs {
				// The delete IS the revocation: the mdt_ auth path is
				// uncached, so the token stops working on the daemon's very
				// next request once this transaction commits.
				if _, err := qtx.DeleteDaemonTokensByWorkspaceAndDaemons(ctx, db.DeleteDaemonTokensByWorkspaceAndDaemonsParams{
					WorkspaceID: parseUUID(wsID),
					DaemonIds:   daemonIDs,
				}); err != nil {
					return empty, err
				}
			}
		}
	}

	// Invalidate the cached account verdict BEFORE committing: if Redis
	// refuses the delete we roll back and nothing changed, so the admin's
	// retry re-runs the whole convergence. Clearing a cache entry for a row
	// the rollback then restores is harmless — it repopulates on read.
	if err := h.invalidateRevocationCaches(ctx, uuidToString(userID)); err != nil {
		return empty, fmt.Errorf("revoking cached verdicts: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return empty, err
	}
	return result, nil
}

// publishAccountStatusChange runs the post-commit side effects: cache
// invalidation, task-cancellation broadcasts, and live-connection kicks.
// These are best-effort — the DB state is already converged — and must run
// AFTER commit so subscribers can never observe state the transaction might
// still roll back.
// invalidateRevocationCaches clears the account-status cache — the one cached
// verdict a status change must beat (every user-identity auth path consults
// it; the mdt_ daemon-token path is deliberately uncached, so token deletion
// needs no cache choreography). Retried once; a persistent failure is
// returned so the caller can refuse to report a revocation that has not
// actually propagated.
func (h *Handler) invalidateRevocationCaches(ctx context.Context, userIDStr string) error {
	if h.AccountGuard == nil {
		return nil
	}
	if err := h.AccountGuard.Cache.Invalidate(ctx, userIDStr); err != nil {
		return h.AccountGuard.Cache.Invalidate(ctx, userIDStr)
	}
	return nil
}

func (h *Handler) publishAccountStatusChange(ctx context.Context, userIDStr, status string, result accountStatusChangeResult) error {
	if status != auth.AccountStatusSuspended {
		return nil
	}

	if h.TaskService != nil && len(result.Cancelled) > 0 {
		// Group per workspace: BroadcastCancelledTasks takes one workspace.
		byWs := map[string][]db.AgentTaskQueue{}
		for _, t := range result.Cancelled {
			wsID := result.WorkspaceByRuntime[uuidToString(t.RuntimeID)]
			byWs[wsID] = append(byWs[wsID], t)
		}
		for wsID, tasks := range byWs {
			if wsID == "" {
				continue
			}
			h.TaskService.BroadcastCancelledTasks(ctx, wsID, tasks)
		}
	}

	// Both kicks run even if the first fails — then the failures surface
	// together. A relay-publish failure here means sockets on other nodes
	// would keep serving the suspended user, so the caller must not report
	// success on it; the kicks are re-derivable and re-run on retry.
	var kickErrs []error
	if h.DisconnectUser != nil {
		if err := h.DisconnectUser(userIDStr); err != nil {
			kickErrs = append(kickErrs, fmt.Errorf("user connections: %w", err))
		}
	}
	// Sever live daemon WebSockets watching the suspended user's runtimes:
	// the token deletion above only gates NEW daemon connections, while an
	// established socket keeps its cached identity.
	if h.DisconnectDaemonRuntimes != nil && len(result.RuntimeIDStrs) > 0 {
		if err := h.DisconnectDaemonRuntimes(result.RuntimeIDStrs); err != nil {
			kickErrs = append(kickErrs, fmt.Errorf("daemon connections: %w", err))
		}
	}
	return errors.Join(kickErrs...)
}
