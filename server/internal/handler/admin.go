package handler

import (
	"net/http"
	"strings"

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
