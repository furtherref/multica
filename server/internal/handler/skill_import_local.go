package handler

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

const (
	maxLocalUploadedSkills            = 16
	maxLocalUploadedSkillFiles        = 128
	maxLocalUploadedSkillFileBytes    = 1024 * 1024
	maxLocalUploadedSkillTotalBytes   = 8 * 1024 * 1024
	maxLocalUploadedSkillRequestBytes = 32 * 1024 * 1024
)

type ImportLocalSkillSourceRequest struct {
	Type  string `json:"type"`
	Label string `json:"label"`
}

type ImportLocalSkillRequest struct {
	Name        string                        `json:"name"`
	Description string                        `json:"description"`
	Content     string                        `json:"content"`
	Files       []CreateSkillFileRequest      `json:"files,omitempty"`
	Source      ImportLocalSkillSourceRequest `json:"source"`
}

type ImportLocalSkillsRequest struct {
	Skills []ImportLocalSkillRequest `json:"skills"`
}

type ImportLocalSkillCreated struct {
	Skill       SkillWithFilesResponse `json:"skill"`
	SourceLabel string                 `json:"source_label"`
}

type ImportLocalSkillResult struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

type ImportLocalSkillsResponse struct {
	Created []ImportLocalSkillCreated `json:"created"`
	Skipped []ImportLocalSkillResult  `json:"skipped"`
	Failed  []ImportLocalSkillResult  `json:"failed"`
}

var metadataFileNames = map[string]bool{
	"thumbs.db":   true,
	"desktop.ini": true,
}

func normalizeLocalUploadedSkillFilePath(path string) (string, bool) {
	path = strings.ReplaceAll(filepath.ToSlash(path), "\\", "/")
	if path == "" || strings.HasPrefix(path, "/") || strings.Contains(path, "\x00") || isWindowsAbsPath(path) {
		return "", false
	}
	parts := strings.Split(path, "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return "", false
		}
		if strings.HasPrefix(part, ".") {
			return "", false
		}
	}
	cleaned := strings.Join(parts, "/")
	if cleaned == "SKILL.md" {
		return "", false
	}
	if metadataFileNames[strings.ToLower(parts[len(parts)-1])] {
		return "", false
	}
	return cleaned, true
}

func isWindowsAbsPath(path string) bool {
	return len(path) >= 3 && path[1] == ':' && path[2] == '/' &&
		((path[0] >= 'a' && path[0] <= 'z') || (path[0] >= 'A' && path[0] <= 'Z'))
}

func validateLocalImportSkill(skill ImportLocalSkillRequest) string {
	if strings.TrimSpace(skill.Name) == "" {
		return "missing_name"
	}
	if strings.TrimSpace(skill.Content) == "" {
		return "missing_skill_md"
	}
	if len(skill.Content) > maxLocalUploadedSkillFileBytes {
		return "file_too_large"
	}
	if len(skill.Files) > maxLocalUploadedSkillFiles {
		return "too_many_files"
	}

	total := len(skill.Content)
	for _, f := range skill.Files {
		if _, ok := normalizeLocalUploadedSkillFilePath(f.Path); !ok {
			return "invalid_file_path"
		}
		if len(f.Content) > maxLocalUploadedSkillFileBytes {
			return "file_too_large"
		}
		total += len(f.Content)
	}
	if total > maxLocalUploadedSkillTotalBytes {
		return "bundle_too_large"
	}
	return ""
}

func decodeImportLocalSkillsRequest(w http.ResponseWriter, r *http.Request, maxBytes int64) (ImportLocalSkillsRequest, bool) {
	var req ImportLocalSkillsRequest
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return req, false
	}
	return req, true
}

func (h *Handler) ImportLocalSkills(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	creatorID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}
	creatorUUID := parseUUID(creatorID)

	req, ok := decodeImportLocalSkillsRequest(w, r, int64(maxLocalUploadedSkillRequestBytes))
	if !ok {
		return
	}
	if len(req.Skills) == 0 {
		writeError(w, http.StatusBadRequest, "skills is required")
		return
	}
	if len(req.Skills) > maxLocalUploadedSkills {
		writeError(w, http.StatusBadRequest, "too many skills")
		return
	}

	resp := ImportLocalSkillsResponse{
		Created: []ImportLocalSkillCreated{},
		Skipped: []ImportLocalSkillResult{},
		Failed:  []ImportLocalSkillResult{},
	}
	for _, item := range req.Skills {
		item.Name = sanitizeNullBytes(strings.TrimSpace(item.Name))
		item.Description = sanitizeNullBytes(item.Description)
		item.Content = sanitizeNullBytes(item.Content)
		item.Source.Label = sanitizeNullBytes(item.Source.Label)
		for i := range item.Files {
			item.Files[i].Path = sanitizeNullBytes(item.Files[i].Path)
			item.Files[i].Content = sanitizeNullBytes(item.Files[i].Content)
		}

		if reason := validateLocalImportSkill(item); reason != "" {
			resp.Failed = append(resp.Failed, ImportLocalSkillResult{Name: item.Name, Reason: reason})
			continue
		}
		for i := range item.Files {
			item.Files[i].Path, _ = normalizeLocalUploadedSkillFilePath(item.Files[i].Path)
		}

		created, err := h.createSkillWithFiles(r.Context(), skillCreateInput{
			WorkspaceID: workspaceUUID,
			CreatorID:   creatorUUID,
			Name:        item.Name,
			Description: item.Description,
			Content:     item.Content,
			Config: map[string]any{
				"origin": map[string]any{
					"type":  "uploaded_bundle",
					"label": item.Source.Label,
				},
			},
			Files: item.Files,
		})
		if err != nil {
			if isUniqueViolation(err) {
				resp.Skipped = append(resp.Skipped, ImportLocalSkillResult{Name: item.Name, Reason: "already_exists"})
				continue
			}
			resp.Failed = append(resp.Failed, ImportLocalSkillResult{Name: item.Name, Reason: "create_failed"})
			continue
		}

		actorType, actorID := h.resolveActor(r, creatorID, workspaceID)
		h.publish(protocol.EventSkillCreated, workspaceID, actorType, actorID, map[string]any{"skill": created})
		resp.Created = append(resp.Created, ImportLocalSkillCreated{
			Skill:       created,
			SourceLabel: item.Source.Label,
		})
	}

	writeJSON(w, http.StatusOK, resp)
}
