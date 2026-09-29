package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

const testTemplateUUID = "22222222-2222-2222-2222-222222222222"

func setTemplateCLITestServerEnv(t *testing.T, serverURL string) {
	t.Helper()
	t.Setenv("MULTICA_AGENT_ID", "template-test-agent")
	t.Setenv("MULTICA_TASK_ID", "template-test-task")
	t.Setenv("MULTICA_DAEMON_PORT", "")
	t.Setenv("MULTICA_TASK_CONFIG_ROOT", "")
	t.Setenv("MULTICA_SERVER_URL", serverURL)
	t.Setenv("MULTICA_WORKSPACE_ID", "ws-1")
	t.Setenv("MULTICA_TOKEN", "mat_template_test")
}

func newTemplateListTestCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "list"}
	cmd.Flags().String("output", "table", "")
	cmd.Flags().Bool("full-id", false, "")
	return cmd
}

func newTemplateGetTestCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "get"}
	cmd.Flags().String("output", "json", "")
	return cmd
}

func newTemplateCreateTestCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "create"}
	cmd.Flags().String("name", "", "")
	cmd.Flags().String("issue-title", "", "")
	cmd.Flags().String("issue-content", "", "")
	cmd.Flags().String("config", "", "")
	cmd.Flags().String("output", "json", "")
	return cmd
}

func newTemplateUpdateTestCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "update"}
	cmd.Flags().String("name", "", "")
	cmd.Flags().String("issue-title", "", "")
	cmd.Flags().String("issue-content", "", "")
	cmd.Flags().String("config", "", "")
	cmd.Flags().String("output", "json", "")
	return cmd
}

func newTemplateArchiveTestCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "archive"}
	cmd.Flags().String("output", "json", "")
	return cmd
}

func newTemplateUnarchiveTestCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "unarchive"}
	cmd.Flags().String("output", "json", "")
	return cmd
}

func templateSummary(name string, archived bool) map[string]any {
	summary := map[string]any{
		"id":           testTemplateUUID,
		"workspace_id": "ws-1",
		"name":         name,
		"issue_title":  "[Bug] {{summary}}",
		"config":       map[string]any{},
		"created_by":   nil,
		"created_at":   "2026-09-23T00:00:00Z",
		"updated_at":   "2026-09-23T00:00:00Z",
	}
	if archived {
		summary["archived_at"] = "2026-09-24T00:00:00Z"
	}
	return summary
}

func TestTemplateCommandRegisteredAsCoreCommand(t *testing.T) {
	var found *cobra.Command
	for _, c := range rootCmd.Commands() {
		if c.Name() == "template" {
			found = c
			break
		}
	}
	if found == nil {
		t.Fatal("root command does not contain template")
	}
	if found.GroupID != groupCore {
		t.Fatalf("template group = %q, want %q", found.GroupID, groupCore)
	}

	names := make([]string, 0, len(found.Commands()))
	for _, c := range found.Commands() {
		names = append(names, c.Name())
	}
	for _, name := range []string{"create", "list", "get", "update", "archive", "unarchive"} {
		if !containsString(names, name) {
			t.Fatalf("template subcommands = %v, missing %q", names, name)
		}
	}
}

func TestTemplateHelpMentionsSubcommandsAndNoIssueCreateIntegration(t *testing.T) {
	var out bytes.Buffer
	templateCmd.SetOut(&out)
	templateCmd.SetErr(&out)
	if err := templateCmd.Help(); err != nil {
		t.Fatalf("template help: %v", err)
	}
	for _, want := range []string{"create", "list", "get", "update", "archive", "unarchive"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("template help missing %q; rendered:\n%s", want, out.String())
		}
	}
}

func TestRunTemplateListSendsWorkspaceAndPrintsTable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/api/issue-templates" {
			t.Fatalf("path = %q, want /api/issue-templates", r.URL.Path)
		}
		if r.URL.Query().Get("workspace_id") != "ws-1" {
			t.Fatalf("workspace_id = %q, want ws-1", r.URL.Query().Get("workspace_id"))
		}
		if r.URL.Query().Get("include_archived") != "true" {
			t.Fatalf("include_archived = %q, want true", r.URL.Query().Get("include_archived"))
		}
		if r.Header.Get("X-Workspace-ID") != "ws-1" {
			t.Fatalf("X-Workspace-ID = %q, want ws-1", r.Header.Get("X-Workspace-ID"))
		}
		_ = json.NewEncoder(w).Encode([]any{templateSummary("Bug", false)})
	}))
	defer srv.Close()
	setTemplateCLITestServerEnv(t, srv.URL)

	cmd := newTemplateListTestCmd()
	out, err := captureStdout(t, func() error { return runTemplateList(cmd, nil) })
	if err != nil {
		t.Fatalf("runTemplateList: %v", err)
	}
	if !strings.Contains(out, "Bug") || !strings.Contains(out, "[Bug] {{summary}}") {
		t.Fatalf("stdout = %q, want table with name and title", out)
	}
}

func TestRunTemplateListJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]any{templateSummary("Bug", true)})
	}))
	defer srv.Close()
	setTemplateCLITestServerEnv(t, srv.URL)

	cmd := newTemplateListTestCmd()
	_ = cmd.Flags().Set("output", "json")
	out, err := captureStdout(t, func() error { return runTemplateList(cmd, nil) })
	if err != nil {
		t.Fatalf("runTemplateList: %v", err)
	}
	var got []map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode stdout JSON: %v\n%s", err, out)
	}
	if len(got) != 1 || got[0]["name"] != "Bug" || got[0]["archived_at"] == nil {
		t.Fatalf("stdout = %#v, want one archived Bug template", got)
	}
}

func TestRunTemplateGetResolvesNameAndPrintsJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/issue-templates" {
			_ = json.NewEncoder(w).Encode([]any{templateSummary("Bug Report", false)})
			return
		}
		if r.Method != http.MethodGet || r.URL.Path != "/api/issue-templates/"+testTemplateUUID {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":            testTemplateUUID,
			"workspace_id":  "ws-1",
			"name":          "Bug Report",
			"issue_title":   "[Bug] {{summary}}",
			"issue_content": "Describe the bug here.",
			"config":        map[string]any{},
			"created_at":    "2026-09-23T00:00:00Z",
			"updated_at":    "2026-09-23T00:00:00Z",
		})
	}))
	defer srv.Close()
	setTemplateCLITestServerEnv(t, srv.URL)

	cmd := newTemplateGetTestCmd()
	out, err := captureStdout(t, func() error { return runTemplateGet(cmd, []string{"bug report"}) })
	if err != nil {
		t.Fatalf("runTemplateGet: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode stdout JSON: %v\n%s", err, out)
	}
	if got["id"] != testTemplateUUID || got["issue_content"] != "Describe the bug here." {
		t.Fatalf("stdout = %#v, want template detail", got)
	}
}

func TestRunTemplateCreateSendsExpectedRequest(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/issue-templates" {
			t.Fatalf("path = %q, want /api/issue-templates", r.URL.Path)
		}
		if r.Header.Get("X-Workspace-ID") != "ws-1" {
			t.Fatalf("X-Workspace-ID = %q, want ws-1", r.Header.Get("X-Workspace-ID"))
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":            testTemplateUUID,
			"workspace_id":  "ws-1",
			"name":          body["name"],
			"issue_title":   body["issue_title"],
			"issue_content": body["issue_content"],
			"config":        body["config"],
			"created_at":    "2026-09-23T00:00:00Z",
			"updated_at":    "2026-09-23T00:00:00Z",
		})
	}))
	defer srv.Close()
	setTemplateCLITestServerEnv(t, srv.URL)

	cmd := newTemplateCreateTestCmd()
	_ = cmd.Flags().Set("name", "Bug Report")
	_ = cmd.Flags().Set("issue-title", "[Bug] {{summary}}")
	_ = cmd.Flags().Set("issue-content", "Describe the bug here.")
	_ = cmd.Flags().Set("config", `{"variables":[{"name":"summary","type":"text","required":true}]}`)

	out, err := captureStdout(t, func() error { return runTemplateCreate(cmd, nil) })
	if err != nil {
		t.Fatalf("runTemplateCreate: %v", err)
	}
	if body["name"] != "Bug Report" || body["issue_title"] != "[Bug] {{summary}}" {
		t.Fatalf("body = %#v, want name/issue_title", body)
	}
	if _, ok := body["config"]; !ok {
		t.Fatalf("body = %#v, want config", body)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode stdout JSON: %v\n%s", err, out)
	}
	if got["id"] != testTemplateUUID || got["name"] != "Bug Report" {
		t.Fatalf("stdout = %#v, want created template", got)
	}
}

func TestRunTemplateCreateRequiresNameAndTitle(t *testing.T) {
	cmd := newTemplateCreateTestCmd()
	if err := runTemplateCreate(cmd, nil); err == nil || !strings.Contains(err.Error(), "--name is required") {
		t.Fatalf("runTemplateCreate error = %v, want missing name", err)
	}
	_ = cmd.Flags().Set("name", "Bug Report")
	if err := runTemplateCreate(cmd, nil); err == nil || !strings.Contains(err.Error(), "--issue-title is required") {
		t.Fatalf("runTemplateCreate error = %v, want missing title", err)
	}
}

func TestRunTemplateCreateRejectsNonObjectConfig(t *testing.T) {
	cmd := newTemplateCreateTestCmd()
	_ = cmd.Flags().Set("name", "Bug Report")
	_ = cmd.Flags().Set("issue-title", "[Bug] {{summary}}")
	_ = cmd.Flags().Set("config", `[1,2,3]`)
	if err := runTemplateCreate(cmd, nil); err == nil || !strings.Contains(err.Error(), "JSON object") {
		t.Fatalf("runTemplateCreate error = %v, want non-object config rejected", err)
	}
}

func TestRunTemplateUpdateSendsOnlyChangedFields(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Fatalf("method = %s, want PUT", r.Method)
		}
		if r.URL.Path != "/api/issue-templates/"+testTemplateUUID {
			t.Fatalf("path = %q, want template path", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":           testTemplateUUID,
			"workspace_id": "ws-1",
			"name":         body["name"],
			"issue_title":  "[Bug] {{summary}}",
			"config":       map[string]any{},
			"created_at":   "2026-09-23T00:00:00Z",
			"updated_at":   "2026-09-23T00:00:00Z",
		})
	}))
	defer srv.Close()
	setTemplateCLITestServerEnv(t, srv.URL)

	cmd := newTemplateUpdateTestCmd()
	_ = cmd.Flags().Set("name", "Bug Report v2")

	if _, err := captureStdout(t, func() error { return runTemplateUpdate(cmd, []string{testTemplateUUID}) }); err != nil {
		t.Fatalf("runTemplateUpdate: %v", err)
	}
	if len(body) != 1 || body["name"] != "Bug Report v2" {
		t.Fatalf("body = %#v, want only name field", body)
	}
}

func TestRunTemplateUpdateNothingToUpdate(t *testing.T) {
	cmd := newTemplateUpdateTestCmd()
	if err := runTemplateUpdate(cmd, []string{testTemplateUUID}); err == nil || !strings.Contains(err.Error(), "nothing to update") {
		t.Fatalf("runTemplateUpdate error = %v, want nothing-to-update", err)
	}
}

func TestRunTemplateArchiveCallsArchiveEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/issue-templates/"+testTemplateUUID+"/archive" {
			t.Fatalf("path = %q, want archive endpoint", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":           testTemplateUUID,
			"workspace_id": "ws-1",
			"name":         "Bug Report",
			"issue_title":  "[Bug] {{summary}}",
			"config":       map[string]any{},
			"archived_at":  "2026-09-24T00:00:00Z",
			"created_at":   "2026-09-23T00:00:00Z",
			"updated_at":   "2026-09-24T00:00:00Z",
		})
	}))
	defer srv.Close()
	setTemplateCLITestServerEnv(t, srv.URL)

	cmd := newTemplateArchiveTestCmd()
	out, err := captureStdout(t, func() error { return makeTemplateArchiveRun(true)(cmd, []string{testTemplateUUID}) })
	if err != nil {
		t.Fatalf("archive template: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode stdout JSON: %v\n%s", err, out)
	}
	if got["name"] != "Bug Report" || got["archived_at"] != "2026-09-24T00:00:00Z" {
		t.Fatalf("stdout = %#v, want archived template", got)
	}
}

func TestRunTemplateUnarchiveCallsUnarchiveEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/issue-templates/"+testTemplateUUID+"/unarchive" {
			t.Fatalf("path = %q, want unarchive endpoint", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":           testTemplateUUID,
			"workspace_id": "ws-1",
			"name":         "Bug Report",
			"issue_title":  "[Bug] {{summary}}",
			"config":       map[string]any{},
			"created_at":   "2026-09-23T00:00:00Z",
			"updated_at":   "2026-09-24T00:00:00Z",
		})
	}))
	defer srv.Close()
	setTemplateCLITestServerEnv(t, srv.URL)

	cmd := newTemplateUnarchiveTestCmd()
	out, err := captureStdout(t, func() error { return makeTemplateArchiveRun(false)(cmd, []string{testTemplateUUID}) })
	if err != nil {
		t.Fatalf("unarchive template: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode stdout JSON: %v\n%s", err, out)
	}
	if got["name"] != "Bug Report" {
		t.Fatalf("stdout = %#v, want restored template", got)
	}
}

func TestResolveTemplateRefByName(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/issue-templates" {
			_ = json.NewEncoder(w).Encode([]any{templateSummary("Bug Report", false)})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	setTemplateCLITestServerEnv(t, srv.URL)

	client, err := newAPIClient(&cobra.Command{})
	if err != nil {
		t.Fatalf("newAPIClient: %v", err)
	}
	ref, err := resolveTemplateRef(t.Context(), client, "bug report")
	if err != nil {
		t.Fatalf("resolveTemplateRef: %v", err)
	}
	if ref.ID != testTemplateUUID || ref.Display != "Bug Report" {
		t.Fatalf("ref = %#v, want id %s / display Bug Report", ref, testTemplateUUID)
	}
}

func TestResolveTemplateRefByPrefixAndUnknown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/issue-templates" {
			_ = json.NewEncoder(w).Encode([]any{templateSummary("Bug Report", false)})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	setTemplateCLITestServerEnv(t, srv.URL)

	client, err := newAPIClient(&cobra.Command{})
	if err != nil {
		t.Fatalf("newAPIClient: %v", err)
	}

	ref, err := resolveTemplateRef(t.Context(), client, "22222222")
	if err != nil {
		t.Fatalf("resolveTemplateRef prefix: %v", err)
	}
	if ref.ID != testTemplateUUID {
		t.Fatalf("ref = %#v, want id %s", ref, testTemplateUUID)
	}

	_, err = resolveTemplateRef(t.Context(), client, "nope")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("resolveTemplateRef error = %v, want not found", err)
	}
}

func containsString(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}
