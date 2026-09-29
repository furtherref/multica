package handler

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type IssueTemplateResponse struct {
	ID           string  `json:"id"`
	WorkspaceID  string  `json:"workspace_id"`
	Name         string  `json:"name"`
	IssueTitle   string  `json:"issue_title"`
	IssueContent string  `json:"issue_content"`
	Config       any     `json:"config"`
	CreatedBy    *string `json:"created_by"`
	Archived     bool    `json:"archived"`
	ArchivedAt   *string `json:"archived_at"`
	CreatedAt    string  `json:"created_at"`
	UpdatedAt    string  `json:"updated_at"`
}

type IssueTemplateSummaryResponse struct {
	ID          string  `json:"id"`
	WorkspaceID string  `json:"workspace_id"`
	Name        string  `json:"name"`
	IssueTitle  string  `json:"issue_title"`
	Config      any     `json:"config"`
	CreatedBy   *string `json:"created_by"`
	Archived    bool    `json:"archived"`
	ArchivedAt  *string `json:"archived_at"`
	CreatedAt   string  `json:"created_at"`
	UpdatedAt   string  `json:"updated_at"`
}

type CreateIssueTemplateRequest struct {
	Name         string `json:"name"`
	IssueTitle   string `json:"issue_title"`
	IssueContent string `json:"issue_content"`
	Config       any    `json:"config"`
}

type UpdateIssueTemplateRequest struct {
	Name         *string `json:"name"`
	IssueTitle   *string `json:"issue_title"`
	IssueContent *string `json:"issue_content"`
	Config       any     `json:"config"`
}

func issueTemplateToResponse(t db.IssueTemplate) IssueTemplateResponse {
	resp := IssueTemplateResponse{
		ID:           uuidToString(t.ID),
		WorkspaceID:  uuidToString(t.WorkspaceID),
		Name:         t.Name,
		IssueTitle:   t.IssueTitle,
		IssueContent: t.IssueContent,
		Config:       decodeSkillConfig(t.Config),
		CreatedBy:    uuidToPtr(t.CreatedBy),
		Archived:     t.ArchivedAt.Valid,
		CreatedAt:    timestampToString(t.CreatedAt),
		UpdatedAt:    timestampToString(t.UpdatedAt),
	}
	if t.ArchivedAt.Valid {
		s := timestampToString(t.ArchivedAt)
		resp.ArchivedAt = &s
	}
	return resp
}

func validateIssueTemplateFields(name, issueTitle string) (string, string, bool) {
	trimmedName := strings.TrimSpace(name)
	trimmedTitle := strings.TrimSpace(issueTitle)
	return trimmedName, trimmedTitle, trimmedName != "" && trimmedTitle != ""
}

func (h *Handler) loadIssueTemplateForUser(w http.ResponseWriter, r *http.Request, id string) (db.IssueTemplate, bool) {
	workspaceID := h.resolveWorkspaceID(r)
	if workspaceID == "" {
		writeError(w, http.StatusBadRequest, "workspace_id is required")
		return db.IssueTemplate{}, false
	}

	templateID, ok := parseUUIDOrBadRequest(w, id, "id")
	if !ok {
		return db.IssueTemplate{}, false
	}
	workspaceUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return db.IssueTemplate{}, false
	}

	template, err := h.Queries.GetIssueTemplateInWorkspace(r.Context(), db.GetIssueTemplateInWorkspaceParams{
		ID:          templateID,
		WorkspaceID: workspaceUUID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "issue template not found")
		return template, false
	}
	return template, true
}

func issueTemplateSummaryToResponse(t db.ListIssueTemplateSummariesByWorkspaceRow) IssueTemplateSummaryResponse {
	return IssueTemplateSummaryResponse{
		ID:          uuidToString(t.ID),
		WorkspaceID: uuidToString(t.WorkspaceID),
		Name:        t.Name,
		IssueTitle:  t.IssueTitle,
		Config:      decodeSkillConfig(t.Config),
		CreatedBy:   uuidToPtr(t.CreatedBy),
		CreatedAt:   timestampToString(t.CreatedAt),
		UpdatedAt:   timestampToString(t.UpdatedAt),
	}
}

func issueTemplateSummaryIncludingArchivedToResponse(t db.ListIssueTemplateSummariesIncludingArchivedByWorkspaceRow) IssueTemplateSummaryResponse {
	resp := IssueTemplateSummaryResponse{
		ID:          uuidToString(t.ID),
		WorkspaceID: uuidToString(t.WorkspaceID),
		Name:        t.Name,
		IssueTitle:  t.IssueTitle,
		Config:      decodeSkillConfig(t.Config),
		CreatedBy:   uuidToPtr(t.CreatedBy),
		Archived:    t.ArchivedAt.Valid,
		CreatedAt:   timestampToString(t.CreatedAt),
		UpdatedAt:   timestampToString(t.UpdatedAt),
	}
	if t.ArchivedAt.Valid {
		s := timestampToString(t.ArchivedAt)
		resp.ArchivedAt = &s
	}
	return resp
}

func (h *Handler) ListIssueTemplates(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	workspaceUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}

	// Default list is active-only; the management page passes include_archived
	// to surface archived templates for unarchiving.
	if r.URL.Query().Get("include_archived") == "true" {
		templates, err := h.Queries.ListIssueTemplateSummariesIncludingArchivedByWorkspace(r.Context(), workspaceUUID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list issue templates")
			return
		}
		resp := make([]IssueTemplateSummaryResponse, len(templates))
		for i, template := range templates {
			resp[i] = issueTemplateSummaryIncludingArchivedToResponse(template)
		}
		writeJSON(w, http.StatusOK, resp)
		return
	}

	templates, err := h.Queries.ListIssueTemplateSummariesByWorkspace(r.Context(), workspaceUUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list issue templates")
		return
	}

	resp := make([]IssueTemplateSummaryResponse, len(templates))
	for i, template := range templates {
		resp[i] = issueTemplateSummaryToResponse(template)
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) GetIssueTemplate(w http.ResponseWriter, r *http.Request) {
	template, ok := h.loadIssueTemplateForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, issueTemplateToResponse(template))
}

func (h *Handler) CreateIssueTemplate(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	workspaceUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}
	creatorID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	if _, ok := h.requireWorkspaceRole(w, r, workspaceID, "workspace not found", "owner", "admin", "member"); !ok {
		return
	}

	var req CreateIssueTemplateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	name, issueTitle, ok := validateIssueTemplateFields(req.Name, req.IssueTitle)
	if !ok {
		writeError(w, http.StatusBadRequest, "name and issue_title are required")
		return
	}

	config, err := json.Marshal(req.Config)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid config")
		return
	}
	if req.Config == nil {
		config = []byte("{}")
	} else if len(config) == 0 || config[0] != '{' {
		writeError(w, http.StatusBadRequest, "config must be a JSON object")
		return
	}

	template, err := h.Queries.CreateIssueTemplate(r.Context(), db.CreateIssueTemplateParams{
		WorkspaceID:  workspaceUUID,
		Name:         sanitizeNullBytes(name),
		IssueTitle:   sanitizeNullBytes(issueTitle),
		IssueContent: sanitizeNullBytes(req.IssueContent),
		Config:       config,
		CreatedBy:    parseUUID(creatorID),
	})
	if err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "an issue template with this name already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to create issue template")
		return
	}

	resp := issueTemplateToResponse(template)
	wsID := uuidToString(workspaceUUID)
	h.publish(protocol.EventIssueTemplateCreated, wsID, "member", creatorID, map[string]any{"issue_template": resp})
	writeJSON(w, http.StatusCreated, resp)
}

func (h *Handler) canManageIssueTemplate(w http.ResponseWriter, r *http.Request, template db.IssueTemplate) bool {
	wsID := uuidToString(template.WorkspaceID)
	member, ok := h.requireWorkspaceRole(w, r, wsID, "issue template not found", "owner", "admin", "member")
	if !ok {
		return false
	}
	isAdmin := roleAllowed(member.Role, "owner", "admin")
	isCreator := template.CreatedBy.Valid && uuidToString(template.CreatedBy) == requestUserID(r)
	if !isAdmin && !isCreator {
		writeError(w, http.StatusForbidden, "only the issue template creator can manage this issue template")
		return false
	}
	return true
}

func (h *Handler) UpdateIssueTemplate(w http.ResponseWriter, r *http.Request) {
	template, ok := h.loadIssueTemplateForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	if !h.canManageIssueTemplate(w, r, template) {
		return
	}

	var req UpdateIssueTemplateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	params := db.UpdateIssueTemplateParams{ID: template.ID}
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			writeError(w, http.StatusBadRequest, "name is required")
			return
		}
		params.Name = pgtype.Text{String: sanitizeNullBytes(name), Valid: true}
	}
	if req.IssueTitle != nil {
		issueTitle := strings.TrimSpace(*req.IssueTitle)
		if issueTitle == "" {
			writeError(w, http.StatusBadRequest, "issue_title is required")
			return
		}
		params.IssueTitle = pgtype.Text{String: sanitizeNullBytes(issueTitle), Valid: true}
	}
	if req.IssueContent != nil {
		params.IssueContent = pgtype.Text{String: sanitizeNullBytes(*req.IssueContent), Valid: true}
	}
	if req.Config != nil {
		config, err := json.Marshal(req.Config)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid config")
			return
		}
		if len(config) == 0 || config[0] != '{' {
			writeError(w, http.StatusBadRequest, "config must be a JSON object")
			return
		}
		params.Config = config
	}

	template, err := h.Queries.UpdateIssueTemplate(r.Context(), params)
	if err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "an issue template with this name already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to update issue template")
		return
	}

	resp := issueTemplateToResponse(template)
	h.publish(protocol.EventIssueTemplateUpdated, uuidToString(template.WorkspaceID), "member", requestUserID(r), map[string]any{"issue_template": resp})
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) ArchiveIssueTemplate(w http.ResponseWriter, r *http.Request) {
	template, ok := h.loadIssueTemplateForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	if !h.canManageIssueTemplate(w, r, template) {
		return
	}
	if template.ArchivedAt.Valid {
		writeError(w, http.StatusConflict, "issue template is already archived")
		return
	}

	archived, err := h.Queries.ArchiveIssueTemplate(r.Context(), db.ArchiveIssueTemplateParams{
		ID:          template.ID,
		WorkspaceID: template.WorkspaceID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to archive issue template")
		return
	}

	resp := issueTemplateToResponse(archived)
	h.publish(protocol.EventIssueTemplateUpdated, uuidToString(archived.WorkspaceID), "member", requestUserID(r), map[string]any{"issue_template": resp})
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) UnarchiveIssueTemplate(w http.ResponseWriter, r *http.Request) {
	template, ok := h.loadIssueTemplateForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	if !h.canManageIssueTemplate(w, r, template) {
		return
	}
	if !template.ArchivedAt.Valid {
		writeError(w, http.StatusConflict, "issue template is not archived")
		return
	}

	unarchived, err := h.Queries.UnarchiveIssueTemplate(r.Context(), db.UnarchiveIssueTemplateParams{
		ID:          template.ID,
		WorkspaceID: template.WorkspaceID,
	})
	if err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "an active issue template with this name already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to unarchive issue template")
		return
	}

	resp := issueTemplateToResponse(unarchived)
	h.publish(protocol.EventIssueTemplateUpdated, uuidToString(unarchived.WorkspaceID), "member", requestUserID(r), map[string]any{"issue_template": resp})
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) DeleteIssueTemplate(w http.ResponseWriter, r *http.Request) {
	template, ok := h.loadIssueTemplateForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	if !h.canManageIssueTemplate(w, r, template) {
		return
	}

	if err := h.Queries.DeleteIssueTemplate(r.Context(), template.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete issue template")
		return
	}
	h.publish(protocol.EventIssueTemplateDeleted, uuidToString(template.WorkspaceID), "member", requestUserID(r), map[string]any{"issue_template_id": uuidToString(template.ID)})
	w.WriteHeader(http.StatusNoContent)
}
