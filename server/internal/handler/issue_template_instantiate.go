package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/logger"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// issueTemplateConfig is the decoded shape of an issue template's `config`
// JSONB blob. Templates declare the {{variable}} keys their title/content may
// reference and the defaults the instantiate endpoint applies while pre-filling
// a new-issue payload. Everything here is optional; a template with an empty
// config behaves like the plain copy path — no interpolation, no defaults.
//
// The wire shape is deliberately loose (`json.RawMessage` for the bags) so a
// template edited by an older client never fails to instantiate: unknown keys
// and wrong-typed values are ignored rather than rejected, and only the keys a
// caller actually relies on are validated.
type issueTemplateConfig struct {
	// Variables maps a declared variable name to its definition. A variable may
	// appear in the template's title/content as {{name}} (whitespace inside the
	// braces tolerated). Undeclared tokens are rejected at instantiate time.
	Variables map[string]issueTemplateVariable `json:"variables"`
	// Defaults pre-fills the returned issue payload. Values are validated
	// against live workspace state (assignee, property definitions, labels) the
	// same way issue creation would validate them, so a stale template surfaces
	// its drift when instantiated instead of when created.
	Defaults issueTemplateDefaults `json:"defaults"`
}

type issueTemplateVariable struct {
	Label    string `json:"label"`
	Required bool   `json:"required"`
	Default  any    `json:"default"`
}

type issueTemplateDefaults struct {
	Priority            string                     `json:"priority"`
	Status              string                     `json:"status"`
	AssigneeType        string                     `json:"assignee_type"`
	AssigneeID          string                     `json:"assignee_id"`
	ProjectID           string                     `json:"project_id"`
	Stage               *int32                     `json:"stage"`
	StartDateOffsetDays *int32                     `json:"start_date_offset_days"`
	DueDateOffsetDays   *int32                     `json:"due_date_offset_days"`
	Properties          map[string]json.RawMessage `json:"properties"`
	Metadata            map[string]json.RawMessage `json:"metadata"`
	Labels              []string                   `json:"labels"`
}

// instantiateIssueTemplateRequest is the body of POST
// /api/issue-templates/{id}/instantiate. `variables` supplies concrete values
// for the template's declared variables. Supplying a variable the template does
// not declare is rejected (redundant), as is omitting a required declared
// variable.
type instantiateIssueTemplateRequest struct {
	Variables map[string]any `json:"variables"`
}

const (
	maxTemplateVariableNameLen  = 64
	maxTemplateVariableValueLen = 5000
)

// templateVariableTokenRE matches any {{...}} token in template title/content.
// Whitespace inside the braces ({{ name }}) is tolerated and the canonical name
// is the trimmed interior, mirroring the autopilot interpolation contract. The
// token must be non-empty after trimming.
var templateVariableTokenRE = regexp.MustCompile(`\{\{\s*([^{}]*?)\s*\}\}`)

// validateTemplateVariableName checks a declared variable name. Names must be
// non-empty, bounded, and free of characters that would make the {{name}}
// token ambiguous in issue text.
func validateTemplateVariableName(name string) error {
	if name == "" {
		return errors.New("variable name is required")
	}
	if len(name) > maxTemplateVariableNameLen {
		return fmt.Errorf("variable name must be %d characters or fewer", maxTemplateVariableNameLen)
	}
	if strings.ContainsAny(name, "{}") {
		return errors.New("variable name must not contain '{' or '}'")
	}
	if strings.TrimSpace(name) != name {
		return errors.New("variable name must not have leading or trailing whitespace")
	}
	return nil
}

// validateTemplateVariableValue bounds a supplied (or defaulted) variable value
// so interpolating it cannot balloon an issue payload. Any JSON scalar is
// acceptable — the value is stringified for interpolation.
func validateTemplateVariableValue(name string, v any) error {
	switch val := v.(type) {
	case string:
		if len(val) > maxTemplateVariableValueLen {
			return fmt.Errorf("value for variable %q must be %d characters or fewer", name, maxTemplateVariableValueLen)
		}
	case json.Number, float64, bool, nil:
		// fine — stringified later
	default:
		return fmt.Errorf("value for variable %q must be a JSON scalar", name)
	}
	return nil
}

// templateVariableValueString renders a variable value as the string that gets
// substituted into {{name}} tokens. null renders as the empty string so an
// optional variable can be explicitly cleared.
func templateVariableValueString(name string, v any) (string, error) {
	if v == nil {
		return "", nil
	}
	switch val := v.(type) {
	case string:
		return val, nil
	case json.Number:
		return val.String(), nil
	case float64:
		b, _ := json.Marshal(val)
		return string(b), nil
	case bool:
		if val {
			return "true", nil
		}
		return "false", nil
	default:
		return "", fmt.Errorf("value for variable %q must be a JSON scalar", name)
	}
}

// collectTemplateVariableTokens returns the canonical names of every {{...}}
// token in the given template text, in order, de-duplicated. Empty tokens
// ({{  }}) are skipped — they are not a variable reference.
func collectTemplateVariableTokens(text string) []string {
	seen := make(map[string]struct{})
	var names []string
	for _, m := range templateVariableTokenRE.FindAllStringSubmatch(text, -1) {
		name := strings.TrimSpace(m[1])
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	return names
}

// interpolateTemplateVariables replaces every {{name}} token in text with the
// resolved value map. Every token referenced in the text is guaranteed to be in
// values by the caller (validateTemplateVariablesAgainstTokens + unresolved
// coverage check), so a stray token means a bug — return it verbatim rather
// than silently dropping it.
func interpolateTemplateVariables(text string, values map[string]string) string {
	return templateVariableTokenRE.ReplaceAllStringFunc(text, func(match string) string {
		name := strings.TrimSpace(match[2 : len(match)-2])
		if v, ok := values[name]; ok {
			return v
		}
		return match
	})
}

// decodedTemplateConfig parses a template's config JSONB blob. Empty or
// unparseable configs decode to an empty struct so instantiation degrades to
// the plain copy path. The blob is created with a JSON-object CHECK, so this
// is defense in depth, not the primary contract.
func decodedTemplateConfig(raw []byte) (issueTemplateConfig, error) {
	var cfg issueTemplateConfig
	if len(raw) == 0 || string(raw) == "{}" || string(raw) == "null" {
		return cfg, nil
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return cfg, fmt.Errorf("invalid template config: %w", err)
	}
	return cfg, nil
}

// normalizeVariableValues builds a name → value map for interpolation,
// reconciling three inputs:
//
//  1. declared variable defaults,
//  2. variables supplied in the request body,
//  3. required declarations with no value anywhere → error (missing),
//  4. supplied names that are not declared → error (redundant).
//
// Optional declared variables with neither a default nor a supplied value are
// simply absent from the map; the caller then rejects any {{token}} that still
// references one of them.
func normalizeVariableValues(declared map[string]issueTemplateVariable, vars map[string]any) (map[string]string, error) {
	values := make(map[string]string, len(declared))

	for name, def := range declared {
		if def.Default != nil {
			if err := validateTemplateVariableValue(name, def.Default); err != nil {
				return nil, err
			}
			str, err := templateVariableValueString(name, def.Default)
			if err != nil {
				return nil, err
			}
			values[name] = str
		}
	}

	var surplus []string
	for name := range vars {
		if _, ok := declared[name]; !ok {
			surplus = append(surplus, name)
		}
	}
	if len(surplus) > 0 {
		sort.Strings(surplus)
		return nil, fmt.Errorf("unknown template variable(s) %s; declared variables: %s",
			strings.Join(surplus, ", "), declaredNames(declared))
	}

	for name, v := range vars {
		if err := validateTemplateVariableValue(name, v); err != nil {
			return nil, err
		}
		str, err := templateVariableValueString(name, v)
		if err != nil {
			return nil, err
		}
		values[name] = str
	}

	var missing []string
	for name, def := range declared {
		if !def.Required {
			continue
		}
		if _, ok := values[name]; !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, fmt.Errorf("missing required template variable(s): %s", strings.Join(missing, ", "))
	}

	return values, nil
}

func declaredNames(m map[string]issueTemplateVariable) string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}

// validateTemplateVariablesAgainstTokens rejects any {{name}} token in the
// title/content that is not a declared variable. A template that ships a token
// with no matching declaration is a template editing error and surfaces at
// instantiate time.
func validateTemplateVariablesAgainstTokens(title, content string, declared map[string]issueTemplateVariable) error {
	for _, token := range collectTemplateVariableTokens(title) {
		if _, ok := declared[token]; !ok {
			return fmt.Errorf("title references undeclared template variable %q", token)
		}
	}
	for _, token := range collectTemplateVariableTokens(content) {
		if _, ok := declared[token]; !ok {
			return fmt.Errorf("content references undeclared template variable %q", token)
		}
	}
	return nil
}

// validateUnresolvedTokens rejects {{name}} tokens that ARE declared but have no
// concrete value after defaults + supplied values are applied. This is the
// "illegal variable" case that interpolation alone would silently pass through
// by leaving the literal token in the payload.
func validateUnresolvedTokens(title, content string, values map[string]string) error {
	for _, token := range collectTemplateVariableTokens(title) {
		if _, ok := values[token]; !ok {
			return fmt.Errorf("title variable %q has no value; supply it or add a default", token)
		}
	}
	for _, token := range collectTemplateVariableTokens(content) {
		if _, ok := values[token]; !ok {
			return fmt.Errorf("content variable %q has no value; supply it or add a default", token)
		}
	}
	return nil
}

// validateTemplateDeclaredVariables validates every declared variable name.
func validateTemplateDeclaredVariables(cfg issueTemplateConfig) error {
	for name := range cfg.Variables {
		if err := validateTemplateVariableName(name); err != nil {
			return err
		}
	}
	return nil
}

// prefilledIssuePayload is the response body of the instantiate endpoint. It
// mirrors the CreateIssueRequest wire shape so a client can feed it directly
// into the create-issue form (and ultimately POST /api/issues), with the
// template identity and resolved variables attached for context.
type prefilledIssuePayload struct {
	TemplateID string               `json:"template_id"`
	Name       string               `json:"name"`
	Issue      prefilledIssueFields `json:"issue"`
	Variables  map[string]string    `json:"variables,omitempty"`
	Unresolved []string             `json:"unresolved_variables"`
}

type prefilledIssueFields struct {
	Title        string                     `json:"title"`
	Description  string                     `json:"description"`
	Priority     *string                    `json:"priority"`
	Status       *string                    `json:"status"`
	AssigneeType *string                    `json:"assignee_type"`
	AssigneeID   *string                    `json:"assignee_id"`
	ProjectID    *string                    `json:"project_id"`
	Stage        *int32                     `json:"stage"`
	StartDate    *string                    `json:"start_date"`
	DueDate      *string                    `json:"due_date"`
	LabelIDs     []string                   `json:"label_ids,omitempty"`
	Properties   map[string]json.RawMessage `json:"properties,omitempty"`
	Metadata     map[string]any             `json:"metadata,omitempty"`
}

// validateInstantiateDefaults performs the "real-time" validation of the
// template's configured defaults against the workspace: assignee existence and
// permission, property definitions and values, label existence/scoping, project
// existence, priority/status enums, and stage bounds. All checks are read-only —
// the handler performs no write, publishes no event, and returns the same
// payload on every call for identical inputs (idempotent).
func (h *Handler) validateInstantiateDefaults(
	w http.ResponseWriter,
	r *http.Request,
	workspaceID string,
	workspaceUUID pgtype.UUID,
	defaults issueTemplateDefaults,
) (prefilledIssueFields, bool) {
	var out prefilledIssueFields

	// Priority is a fixed enum; status resolves against the workspace status
	// catalog exactly as issue creation would.
	if defaults.Priority != "" {
		if !containsTemplatePriority(validIssuePriorities, defaults.Priority) {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid default priority %q; valid values: %s",
				defaults.Priority, strings.Join(validIssuePriorities, ", ")))
			return out, false
		}
		out.Priority = &defaults.Priority
	}
	if defaults.Status != "" {
		resolved, ok := h.resolveIssueStatusKey(w, r, workspaceUUID, defaults.Status)
		if !ok {
			return out, false
		}
		out.Status = &resolved
	}

	// Assignee pair must validate against live workspace state — same gate as
	// issue creation. An archived agent/squad or a member that left the
	// workspace fails here.
	if defaults.AssigneeType != "" || defaults.AssigneeID != "" {
		if (defaults.AssigneeType == "") != (defaults.AssigneeID == "") {
			writeError(w, http.StatusBadRequest, "default assignee_type and assignee_id must be provided together")
			return out, false
		}
		assigneeType := pgtype.Text{String: defaults.AssigneeType, Valid: true}
		assigneeID, ok := parseUUIDOrBadRequest(w, defaults.AssigneeID, "default assignee_id")
		if !ok {
			return out, false
		}
		if status, msg := h.validateAssigneePair(r.Context(), r, workspaceID, assigneeType, assigneeID); status != 0 {
			writeError(w, status, msg)
			return out, false
		}
		out.AssigneeType = &defaults.AssigneeType
		out.AssigneeID = &defaults.AssigneeID
	}

	// Project must exist in this workspace.
	if defaults.ProjectID != "" {
		projectUUID, ok := parseUUIDOrBadRequest(w, defaults.ProjectID, "default project_id")
		if !ok {
			return out, false
		}
		if _, err := h.Queries.GetProjectInWorkspace(r.Context(), db.GetProjectInWorkspaceParams{
			ID:          projectUUID,
			WorkspaceID: workspaceUUID,
		}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeError(w, http.StatusBadRequest, "default project_id does not refer to a project in this workspace")
				return out, false
			}
			slog.Warn("validate project default", append(logger.RequestAttrs(r), "error", err)...)
			writeError(w, http.StatusInternalServerError, "failed to validate project default")
			return out, false
		}
		out.ProjectID = &defaults.ProjectID
	}

	// Stage must be >= 1 (matches the create path).
	if defaults.Stage != nil && *defaults.Stage < 1 {
		writeError(w, http.StatusBadRequest, "default stage must be >= 1")
		return out, false
	}
	out.Stage = defaults.Stage

	// Date offsets are relative to today, in days. Only present when set.
	if defaults.StartDateOffsetDays != nil {
		start := dateAfterDays(time.Now(), *defaults.StartDateOffsetDays)
		out.StartDate = &start
	}
	if defaults.DueDateOffsetDays != nil {
		due := dateAfterDays(time.Now(), *defaults.DueDateOffsetDays)
		out.DueDate = &due
	}

	// Property defaults validate against their live definitions. A property
	// that was archived or deleted since the template was authored fails here.
	if len(defaults.Properties) > 0 {
		validated, ok := h.validatePrefillProperties(w, r, workspaceUUID, defaults.Properties)
		if !ok {
			return out, false
		}
		out.Properties = validated
	}

	// Metadata defaults must satisfy the per-issue metadata key/value rules.
	if len(defaults.Metadata) > 0 {
		metadata, ok := validatePrefillMetadata(w, defaults.Metadata)
		if !ok {
			return out, false
		}
		out.Metadata = metadata
	}

	// Label defaults must exist and be issue-scoped in this workspace.
	if len(defaults.Labels) > 0 {
		labels, ok := h.validatePrefillLabels(w, r, workspaceUUID, defaults.Labels)
		if !ok {
			return out, false
		}
		out.LabelIDs = labels
	}

	return out, true
}

func (h *Handler) validatePrefillProperties(
	w http.ResponseWriter,
	r *http.Request,
	workspaceUUID pgtype.UUID,
	values map[string]json.RawMessage,
) (map[string]json.RawMessage, bool) {
	parsed, ok := parseIssueCreateProperties(w, values)
	if !ok {
		return nil, false
	}
	out := make(map[string]json.RawMessage, len(parsed))
	for id, raw := range parsed {
		def, err := h.Queries.GetIssueProperty(r.Context(), db.GetIssuePropertyParams{
			ID:          id,
			WorkspaceID: workspaceUUID,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeError(w, http.StatusBadRequest, "default property not found in this workspace")
				return nil, false
			}
			slog.Warn("validate property default", append(logger.RequestAttrs(r), "error", err)...)
			writeError(w, http.StatusInternalServerError, "failed to validate property default")
			return nil, false
		}
		if def.ArchivedAt.Valid {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("default property %q is archived and cannot receive new values", def.Name))
			return nil, false
		}
		value, err := validatePropertyValue(def, raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid default value for property %q: %s", def.Name, err.Error()))
			return nil, false
		}
		if propertyTypeIsActor(def.Type) {
			refs, err := actorRefsInValue(def.Type, value)
			if err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return nil, false
			}
			if status, msg := h.resolveActorRefs(r, uuidToString(workspaceUUID), refs); status != 0 {
				writeError(w, status, msg)
				return nil, false
			}
		}
		out[uuidToString(id)] = value
	}
	return out, true
}

func validatePrefillMetadata(w http.ResponseWriter, values map[string]json.RawMessage) (map[string]any, bool) {
	if len(values) > maxIssueMetadataKeys {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("default metadata exceeds %d keys", maxIssueMetadataKeys))
		return nil, false
	}
	out := make(map[string]any, len(values))
	for key, raw := range values {
		if err := validateIssueMetadataKey(key); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid metadata key %q: %s", key, err.Error()))
			return nil, false
		}
		if err := validateIssueMetadataValue(raw); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid metadata value for %q: %s", key, err.Error()))
			return nil, false
		}
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid metadata value for %q", key))
			return nil, false
		}
		out[key] = v
	}
	return out, true
}

func (h *Handler) validatePrefillLabels(
	w http.ResponseWriter,
	r *http.Request,
	workspaceUUID pgtype.UUID,
	labelIDs []string,
) ([]string, bool) {
	parsed, ok := parseUUIDSliceOrBadRequest(w, labelIDs, "default label_ids")
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(parsed))
	seen := make(map[string]struct{}, len(parsed))
	for _, id := range parsed {
		key := uuidToString(id)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		label, err := h.Queries.GetLabel(r.Context(), db.GetLabelParams{
			ID:          id,
			WorkspaceID: workspaceUUID,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeError(w, http.StatusBadRequest, "default label not found in this workspace")
				return nil, false
			}
			slog.Warn("validate label default", append(logger.RequestAttrs(r), "error", err)...)
			writeError(w, http.StatusInternalServerError, "failed to validate label default")
			return nil, false
		}
		if label.ResourceType != "issue" {
			writeError(w, http.StatusBadRequest, "default label is not issue-scoped")
			return nil, false
		}
		out = append(out, key)
	}
	if out == nil {
		out = []string{}
	}
	return out, true
}

func dateAfterDays(now time.Time, days int32) string {
	return now.AddDate(0, 0, int(days)).Format(time.DateOnly)
}

func containsTemplatePriority(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// InstantiateIssueTemplate implements POST /api/issue-templates/{id}/instantiate.
//
// It parses the template's {{variable}} tokens, validates the supplied variable
// values against the declared set (missing / redundant / illegal all fail),
// interpolates concrete values into title and content, applies the template's
// configured defaults with live validation against workspace state, and returns
// a prefilled new-issue payload. It never creates an issue, publishes no event,
// and performs no write — calling it with identical inputs always returns the
// same payload, so it is safe to call repeatedly (idempotent, no side effects).
func (h *Handler) InstantiateIssueTemplate(w http.ResponseWriter, r *http.Request) {
	template, ok := h.loadIssueTemplateForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	workspaceID := uuidToString(template.WorkspaceID)

	cfg, err := decodedTemplateConfig(template.Config)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateTemplateDeclaredVariables(cfg); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateTemplateVariablesAgainstTokens(template.IssueTitle, template.IssueContent, cfg.Variables); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	var req instantiateIssueTemplateRequest
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
	}

	values, err := normalizeVariableValues(cfg.Variables, req.Variables)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateUnresolvedTokens(template.IssueTitle, template.IssueContent, values); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	title := interpolateTemplateVariables(template.IssueTitle, values)
	content := interpolateTemplateVariables(template.IssueContent, values)
	if strings.TrimSpace(title) == "" {
		writeError(w, http.StatusBadRequest, "instantiated issue title is empty")
		return
	}

	fields, ok := h.validateInstantiateDefaults(w, r, workspaceID, template.WorkspaceID, cfg.Defaults)
	if !ok {
		return
	}
	fields.Title = title
	fields.Description = content

	writeJSON(w, http.StatusOK, prefilledIssuePayload{
		TemplateID: uuidToString(template.ID),
		Name:       template.Name,
		Issue:      fields,
		Variables:  values,
		Unresolved: []string{},
	})
}
