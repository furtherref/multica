package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// Pure interpolation logic (no DB)
// ---------------------------------------------------------------------------

func TestNormalizeVariableValues(t *testing.T) {
	tests := []struct {
		name      string
		declared  map[string]issueTemplateVariable
		supplied  map[string]any
		want      map[string]string
		wantError string
	}{
		{
			name: "defaults plus supplied override",
			declared: map[string]issueTemplateVariable{
				"area": {Default: "backend"},
				"sev":  {Required: true},
			},
			supplied: map[string]any{"sev": "P1"},
			want:     map[string]string{"area": "backend", "sev": "P1"},
		},
		{
			name: "supplied overrides default",
			declared: map[string]issueTemplateVariable{
				"area": {Default: "backend"},
			},
			supplied: map[string]any{"area": "frontend"},
			want:     map[string]string{"area": "frontend"},
		},
		{
			name: "required missing fails",
			declared: map[string]issueTemplateVariable{
				"sev": {Required: true},
			},
			wantError: "missing required template variable(s): sev",
		},
		{
			name: "surplus variable fails",
			declared: map[string]issueTemplateVariable{
				"area": {},
			},
			supplied:  map[string]any{"area": "x", "bogus": "y"},
			wantError: "unknown template variable(s) bogus; declared variables: area",
		},
		{
			name: "null clears optional default",
			declared: map[string]issueTemplateVariable{
				"area": {Default: "backend"},
			},
			supplied: map[string]any{"area": nil},
			want:     map[string]string{"area": ""},
		},
		{
			name: "scalar number stringifies",
			declared: map[string]issueTemplateVariable{
				"n": {Default: json.Number("42")},
			},
			want: map[string]string{"n": "42"},
		},
		{
			name: "oversized value fails",
			declared: map[string]issueTemplateVariable{
				"area": {},
			},
			supplied:  map[string]any{"area": strings.Repeat("x", maxTemplateVariableValueLen+1)},
			wantError: "must be 5000 characters or fewer",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeVariableValues(tt.declared, tt.supplied)
			if tt.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantError) {
					t.Fatalf("want error containing %q, got err=%v", tt.wantError, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("map mismatch: got %v want %v", got, tt.want)
			}
			for k, v := range tt.want {
				if got[k] != v {
					t.Fatalf("key %q: got %q want %q", k, got[k], v)
				}
			}
		})
	}
}

func TestCollectAndValidateTokens(t *testing.T) {
	if got := collectTemplateVariableTokens("Hello {{area}} and {{ area }}"); len(got) != 1 || got[0] != "area" {
		t.Fatalf("dedupe/trim: got %v", got)
	}
	if got := collectTemplateVariableTokens("no tokens here"); got != nil {
		t.Fatalf("expected nil, got %v", got)
	}

	declared := map[string]issueTemplateVariable{"area": {}}
	if err := validateTemplateVariablesAgainstTokens("{{area}} x", "", declared); err != nil {
		t.Fatalf("declared token should pass: %v", err)
	}
	if err := validateTemplateVariablesAgainstTokens("{{bogus}} x", "", declared); err == nil {
		t.Fatalf("undeclared token should fail")
	} else if !strings.Contains(err.Error(), "undeclared") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInterpolateTemplateVariables(t *testing.T) {
	values := map[string]string{"area": "frontend", "sev": "P1"}
	got := interpolateTemplateVariables("Fix {{ area }} ({{sev}})", values)
	if got != "Fix frontend (P1)" {
		t.Fatalf("got %q", got)
	}
	// A token missing from the map is preserved verbatim (defense in depth).
	got = interpolateTemplateVariables("{{unknown}} here", values)
	if got != "{{unknown}} here" {
		t.Fatalf("stray token should be preserved, got %q", got)
	}
}

func TestValidateUnresolvedTokens(t *testing.T) {
	values := map[string]string{"area": "x"}
	if err := validateUnresolvedTokens("{{area}} ok", "{{area}} ok", values); err != nil {
		t.Fatalf("resolved tokens should pass: %v", err)
	}
	if err := validateUnresolvedTokens("{{missing}} here", "", values); err == nil {
		t.Fatalf("unresolved declared token should fail")
	}
}

func TestDecodedTemplateConfig(t *testing.T) {
	cfg, err := decodedTemplateConfig(nil)
	if err != nil || len(cfg.Variables) != 0 {
		t.Fatalf("nil config should decode empty: %v %v", cfg, err)
	}
	cfg, err = decodedTemplateConfig([]byte(`{"variables":{"area":{"label":"Area","required":true}},"defaults":{"priority":"high"}}`))
	if err != nil {
		t.Fatalf("valid config failed: %v", err)
	}
	if !cfg.Variables["area"].Required || cfg.Defaults.Priority != "high" {
		t.Fatalf("bad decode: %+v", cfg)
	}
}

// ---------------------------------------------------------------------------
// DB-backed handler tests
// ---------------------------------------------------------------------------

// createIssueTemplateForTest creates a template and returns its id, cleaning up
// on test end.
func createIssueTemplateForTest(t *testing.T, body map[string]any) string {
	t.Helper()
	w := httptest.NewRecorder()
	testHandler.CreateIssueTemplate(w, newRequest(http.MethodPost, "/api/issue-templates?workspace_id="+testWorkspaceID, body))
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssueTemplate status = %d body=%s", w.Code, w.Body.String())
	}
	var created map[string]any
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatalf("created template missing id: %#v", created)
	}
	t.Cleanup(func() {
		req := withURLParam(newRequest(http.MethodDelete, "/api/issue-templates/"+id+"?workspace_id="+testWorkspaceID, nil), "id", id)
		testHandler.DeleteIssueTemplate(httptest.NewRecorder(), req)
	})
	return id
}

func instantiateRequest(t *testing.T, id string, body any) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := withURLParam(newRequest(http.MethodPost, "/api/issue-templates/"+id+"/instantiate?workspace_id="+testWorkspaceID, body), "id", id)
	testHandler.InstantiateIssueTemplate(w, req)
	return w
}

func TestInstantiateIssueTemplateBasic(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	id := createIssueTemplateForTest(t, map[string]any{
		"name":          "Instantiate basic",
		"issue_title":   "[{{area}}] Fix {{sev}} bug",
		"issue_content": "Area: {{area}}\n\n## Steps",
		"config": map[string]any{
			"variables": map[string]any{
				"area": map[string]any{"label": "Area", "required": true},
				"sev":  map[string]any{"label": "Severity"},
			},
			"defaults": map[string]any{"priority": "high"},
		},
	})

	w := instantiateRequest(t, id, map[string]any{
		"variables": map[string]any{"area": "backend", "sev": "P1"},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("instantiate status = %d body=%s", w.Code, w.Body.String())
	}
	var out map[string]any
	if err := json.NewDecoder(w.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	issue, ok := out["issue"].(map[string]any)
	if !ok {
		t.Fatalf("missing issue payload: %#v", out)
	}
	if issue["title"] != "[backend] Fix P1 bug" {
		t.Fatalf("title = %v", issue["title"])
	}
	if issue["description"] != "Area: backend\n\n## Steps" {
		t.Fatalf("description = %v", issue["description"])
	}
	if issue["priority"] != "high" {
		t.Fatalf("priority = %v", issue["priority"])
	}
	if out["template_id"] != id {
		t.Fatalf("template_id = %v", out["template_id"])
	}
}

func TestInstantiateMissingRequiredVariable(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	id := createIssueTemplateForTest(t, map[string]any{
		"name":        "Instantiate missing",
		"issue_title": "{{area}} bug",
		"config": map[string]any{
			"variables": map[string]any{"area": map[string]any{"required": true}},
		},
	})
	w := instantiateRequest(t, id, map[string]any{"variables": map[string]any{}})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "missing required") {
		t.Fatalf("unexpected body: %s", w.Body.String())
	}
}

func TestInstantiateSurplusVariable(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	id := createIssueTemplateForTest(t, map[string]any{
		"name":        "Instantiate surplus",
		"issue_title": "{{area}} bug",
		"config": map[string]any{
			"variables": map[string]any{"area": map[string]any{}},
		},
	})
	w := instantiateRequest(t, id, map[string]any{"variables": map[string]any{"area": "x", "bogus": "y"}})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "unknown template variable") {
		t.Fatalf("unexpected body: %s", w.Body.String())
	}
}

func TestInstantiateUndeclaredToken(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	id := createIssueTemplateForTest(t, map[string]any{
		"name":        "Instantiate undeclared token",
		"issue_title": "{{nope}} bug",
	})
	w := instantiateRequest(t, id, map[string]any{})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "undeclared") {
		t.Fatalf("unexpected body: %s", w.Body.String())
	}
}

func TestInstantiateUnresolvedOptionalToken(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	// Optional variable referenced in the title but never given a value or
	// default → the token would leak into the payload; must fail.
	id := createIssueTemplateForTest(t, map[string]any{
		"name":        "Instantiate unresolved optional",
		"issue_title": "{{area}} bug",
		"config": map[string]any{
			"variables": map[string]any{"area": map[string]any{"label": "Area"}},
		},
	})
	w := instantiateRequest(t, id, map[string]any{})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "no value") {
		t.Fatalf("unexpected body: %s", w.Body.String())
	}
}

func TestInstantiateDefaultAssignees(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := createHandlerTestAgent(t, "Instantiate agent", nil)

	id := createIssueTemplateForTest(t, map[string]any{
		"name":        "Instantiate assignee",
		"issue_title": "Assignee default",
		"config": map[string]any{
			"defaults": map[string]any{
				"assignee_type": "agent",
				"assignee_id":   agentID,
			},
		},
	})
	w := instantiateRequest(t, id, map[string]any{})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var out map[string]any
	json.NewDecoder(w.Body).Decode(&out)
	issue, _ := out["issue"].(map[string]any)
	if issue["assignee_type"] != "agent" || issue["assignee_id"] != agentID {
		t.Fatalf("assignee prefill mismatch: %#v", issue)
	}
}

func TestInstantiateDefaultProjectAndStage(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	projectID := dbfx.Project(t, "Instantiate project")

	id := createIssueTemplateForTest(t, map[string]any{
		"name":        "Instantiate project",
		"issue_title": "Project default",
		"config": map[string]any{
			"defaults": map[string]any{
				"project_id": projectID,
				"stage":      2,
			},
		},
	})
	w := instantiateRequest(t, id, map[string]any{})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var out map[string]any
	json.NewDecoder(w.Body).Decode(&out)
	issue, _ := out["issue"].(map[string]any)
	if issue["project_id"] != projectID {
		t.Fatalf("project_id = %v", issue["project_id"])
	}
	if issue["stage"] != float64(2) {
		t.Fatalf("stage = %v", issue["stage"])
	}
}

func TestInstantiateDefaultInvalidProject(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	id := createIssueTemplateForTest(t, map[string]any{
		"name":        "Instantiate bad project",
		"issue_title": "Bad project",
		"config": map[string]any{
			"defaults": map[string]any{"project_id": "00000000-0000-0000-0000-000000000000"},
		},
	})
	w := instantiateRequest(t, id, map[string]any{})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
}

func TestInstantiateDateOffsets(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	id := createIssueTemplateForTest(t, map[string]any{
		"name":        "Instantiate dates",
		"issue_title": "Date defaults",
		"config": map[string]any{
			"defaults": map[string]any{
				"start_date_offset_days": -1,
				"due_date_offset_days":   7,
			},
		},
	})
	w := instantiateRequest(t, id, map[string]any{})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var out map[string]any
	json.NewDecoder(w.Body).Decode(&out)
	issue, _ := out["issue"].(map[string]any)
	start, _ := issue["start_date"].(string)
	due, _ := issue["due_date"].(string)
	if start == "" || due == "" {
		t.Fatalf("date offsets not applied: %#v", issue)
	}
	if start >= due {
		t.Fatalf("expected start before due: start=%s due=%s", start, due)
	}
}

func TestInstantiateDefaultLabels(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	var labelID string
	if err := testPool.QueryRow(context.Background(),
		`INSERT INTO issue_label (workspace_id, resource_type, name, color)
		 VALUES ($1, 'issue', $2, '#000000') RETURNING id`,
		testWorkspaceID, "instantiate label "+randSuffix(),
	).Scan(&labelID); err != nil {
		t.Fatalf("insert issue label: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM issue_label WHERE id = $1`, labelID)
	})

	id := createIssueTemplateForTest(t, map[string]any{
		"name":        "Instantiate labels",
		"issue_title": "Label defaults",
		"config": map[string]any{
			"defaults": map[string]any{"labels": []any{labelID}},
		},
	})
	w := instantiateRequest(t, id, map[string]any{})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var out map[string]any
	json.NewDecoder(w.Body).Decode(&out)
	issue, _ := out["issue"].(map[string]any)
	labels, ok := issue["label_ids"].([]any)
	if !ok || len(labels) != 1 || labels[0] != labelID {
		t.Fatalf("label_ids mismatch: %#v", issue["label_ids"])
	}
}

func TestInstantiateDefaultProperty(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	propID := insertTestTextProperty(t)

	id := createIssueTemplateForTest(t, map[string]any{
		"name":        "Instantiate property",
		"issue_title": "Property default",
		"config": map[string]any{
			"defaults": map[string]any{
				"properties": map[string]any{propID: "hello"},
			},
		},
	})
	w := instantiateRequest(t, id, map[string]any{})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var out map[string]any
	json.NewDecoder(w.Body).Decode(&out)
	issue, _ := out["issue"].(map[string]any)
	props, ok := issue["properties"].(map[string]any)
	if !ok || props[propID] != "hello" {
		t.Fatalf("properties mismatch: %#v", issue["properties"])
	}
}

func TestInstantiateDefaultInvalidPropertyValue(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	propID := insertTestTextProperty(t)

	id := createIssueTemplateForTest(t, map[string]any{
		"name":        "Instantiate bad property",
		"issue_title": "Bad property",
		"config": map[string]any{
			"defaults": map[string]any{
				"properties": map[string]any{propID: 123},
			},
		},
	})
	w := instantiateRequest(t, id, map[string]any{})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
}

func TestInstantiateDefaultMetadata(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	id := createIssueTemplateForTest(t, map[string]any{
		"name":        "Instantiate metadata",
		"issue_title": "Metadata default",
		"config": map[string]any{
			"defaults": map[string]any{
				"metadata": map[string]any{"pipeline_status": "planned"},
			},
		},
	})
	w := instantiateRequest(t, id, map[string]any{})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var out map[string]any
	json.NewDecoder(w.Body).Decode(&out)
	issue, _ := out["issue"].(map[string]any)
	metadata, ok := issue["metadata"].(map[string]any)
	if !ok || metadata["pipeline_status"] != "planned" {
		t.Fatalf("metadata mismatch: %#v", issue["metadata"])
	}
}

func TestInstantiateIsIdempotentNoSideEffects(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	before := countIssuesInTestWorkspace(t)

	id := createIssueTemplateForTest(t, map[string]any{
		"name":          "Instantiate idempotent",
		"issue_title":   "{{area}} bug",
		"issue_content": "x",
		"config": map[string]any{
			"variables": map[string]any{"area": map[string]any{}},
			"defaults":  map[string]any{"priority": "high"},
		},
	})

	body := map[string]any{"variables": map[string]any{"area": "backend"}}
	w1 := instantiateRequest(t, id, body)
	w2 := instantiateRequest(t, id, body)
	if w1.Code != http.StatusOK || w2.Code != http.StatusOK {
		t.Fatalf("statuses: %d %d", w1.Code, w2.Code)
	}
	if w1.Body.String() != w2.Body.String() {
		t.Fatalf("idempotency violated:\n%s\n%s", w1.Body.String(), w2.Body.String())
	}
	if after := countIssuesInTestWorkspace(t); after != before {
		t.Fatalf("instantiate created issues: before=%d after=%d", before, after)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func insertTestTextProperty(t *testing.T) string {
	t.Helper()
	var id string
	if err := testPool.QueryRow(context.Background(),
		`INSERT INTO issue_property (workspace_id, name, type, config)
		 VALUES ($1, $2, 'text', '{}'::jsonb) RETURNING id`,
		testWorkspaceID, "instantiate-prop-"+randSuffix(),
	).Scan(&id); err != nil {
		t.Fatalf("insert text property: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM issue_property WHERE id = $1`, id)
	})
	return id
}

func randSuffix() string {
	return uuid.NewString()[:8]
}

func countIssuesInTestWorkspace(t *testing.T) int {
	t.Helper()
	var n int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM issue WHERE workspace_id = $1`, testWorkspaceID,
	).Scan(&n); err != nil {
		t.Fatalf("count issues: %v", err)
	}
	return n
}
