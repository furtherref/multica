package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func createIssueTemplateForTest(t *testing.T, name string) string {
	t.Helper()
	createReq := map[string]any{
		"name":        name,
		"issue_title": "Title for " + name,
	}
	w := httptest.NewRecorder()
	testHandler.CreateIssueTemplate(w, newRequest(http.MethodPost, "/api/issue-templates?workspace_id="+testWorkspaceID, createReq))
	if w.Code != http.StatusCreated {
		t.Fatalf("create template %q status = %d body=%s", name, w.Code, w.Body.String())
	}
	var created map[string]any
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatalf("created response missing id: %#v", created)
	}
	return id
}

func TestIssueTemplateArchiveUnarchive(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	id := createIssueTemplateForTest(t, "Archive me")
	t.Cleanup(func() {
		req := withURLParam(newRequest(http.MethodDelete, "/api/issue-templates/"+id+"?workspace_id="+testWorkspaceID, nil), "id", id)
		testHandler.DeleteIssueTemplate(httptest.NewRecorder(), req)
	})

	// Archiving returns the full detail with archived=true.
	w := httptest.NewRecorder()
	req := withURLParam(newRequest(http.MethodPost, "/api/issue-templates/"+id+"/archive?workspace_id="+testWorkspaceID, nil), "id", id)
	testHandler.ArchiveIssueTemplate(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ArchiveIssueTemplate status = %d body=%s", w.Code, w.Body.String())
	}
	var archived map[string]any
	if err := json.NewDecoder(w.Body).Decode(&archived); err != nil {
		t.Fatal(err)
	}
	if archived["archived"] != true {
		t.Fatalf("archived response should mark archived=true: %#v", archived)
	}
	if archived["archived_at"] == nil {
		t.Fatalf("archived response should include archived_at: %#v", archived)
	}

	// The default list must not include the archived template.
	w = httptest.NewRecorder()
	testHandler.ListIssueTemplates(w, newRequest(http.MethodGet, "/api/issue-templates?workspace_id="+testWorkspaceID, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("ListIssueTemplates status = %d", w.Code)
	}
	var list []map[string]any
	if err := json.NewDecoder(w.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	for _, item := range list {
		if item["id"] == id {
			t.Fatalf("archived template still in default list: %#v", item)
		}
	}

	// Get by id still resolves the archived template (detail / admin view).
	w = httptest.NewRecorder()
	req = withURLParam(newRequest(http.MethodGet, "/api/issue-templates/"+id+"?workspace_id="+testWorkspaceID, nil), "id", id)
	testHandler.GetIssueTemplate(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GetIssueTemplate after archive status = %d body=%s", w.Code, w.Body.String())
	}

	// include_archived surfaces it.
	w = httptest.NewRecorder()
	testHandler.ListIssueTemplates(w, newRequest(http.MethodGet, "/api/issue-templates?workspace_id="+testWorkspaceID+"&include_archived=true", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("ListIssueTemplates include_archived status = %d", w.Code)
	}
	var full []map[string]any
	if err := json.NewDecoder(w.Body).Decode(&full); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range full {
		if item["id"] == id {
			found = true
			if item["archived"] != true {
				t.Fatalf("include_archived should mark archived=true: %#v", item)
			}
		}
	}
	if !found {
		t.Fatalf("include_archived list missing archived template %s: %#v", id, full)
	}

	// Unarchive restores it to the active list.
	w = httptest.NewRecorder()
	req = withURLParam(newRequest(http.MethodPost, "/api/issue-templates/"+id+"/unarchive?workspace_id="+testWorkspaceID, nil), "id", id)
	testHandler.UnarchiveIssueTemplate(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("UnarchiveIssueTemplate status = %d body=%s", w.Code, w.Body.String())
	}
	var unarchived map[string]any
	if err := json.NewDecoder(w.Body).Decode(&unarchived); err != nil {
		t.Fatal(err)
	}
	if unarchived["archived"] != false {
		t.Fatalf("unarchived response should mark archived=false: %#v", unarchived)
	}

	w = httptest.NewRecorder()
	testHandler.ListIssueTemplates(w, newRequest(http.MethodGet, "/api/issue-templates?workspace_id="+testWorkspaceID, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("ListIssueTemplates status = %d", w.Code)
	}
	if err := json.NewDecoder(w.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	found = false
	for _, item := range list {
		if item["id"] == id {
			found = true
		}
	}
	if !found {
		t.Fatalf("unarchived template missing from default list: %#v", list)
	}
}

func TestIssueTemplateArchiveIdempotency(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	id := createIssueTemplateForTest(t, "Archive twice")
	t.Cleanup(func() {
		req := withURLParam(newRequest(http.MethodDelete, "/api/issue-templates/"+id+"?workspace_id="+testWorkspaceID, nil), "id", id)
		testHandler.DeleteIssueTemplate(httptest.NewRecorder(), req)
	})

	req := withURLParam(newRequest(http.MethodPost, "/api/issue-templates/"+id+"/archive?workspace_id="+testWorkspaceID, nil), "id", id)
	testHandler.ArchiveIssueTemplate(httptest.NewRecorder(), req)

	// Archiving an archived template conflicts.
	w := httptest.NewRecorder()
	req = withURLParam(newRequest(http.MethodPost, "/api/issue-templates/"+id+"/archive?workspace_id="+testWorkspaceID, nil), "id", id)
	testHandler.ArchiveIssueTemplate(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("re-archive status = %d body=%s (want 409)", w.Code, w.Body.String())
	}

	// Unarchiving an active template conflicts.
	req = withURLParam(newRequest(http.MethodPost, "/api/issue-templates/"+id+"/unarchive?workspace_id="+testWorkspaceID, nil), "id", id)
	testHandler.UnarchiveIssueTemplate(httptest.NewRecorder(), req)
	w = httptest.NewRecorder()
	req = withURLParam(newRequest(http.MethodPost, "/api/issue-templates/"+id+"/unarchive?workspace_id="+testWorkspaceID, nil), "id", id)
	testHandler.UnarchiveIssueTemplate(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("re-unarchive status = %d body=%s (want 409)", w.Code, w.Body.String())
	}
}

func TestIssueTemplateArchiveNameReuseConflict(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	id := createIssueTemplateForTest(t, "Name reuse")
	t.Cleanup(func() {
		req := withURLParam(newRequest(http.MethodDelete, "/api/issue-templates/"+id+"?workspace_id="+testWorkspaceID, nil), "id", id)
		testHandler.DeleteIssueTemplate(httptest.NewRecorder(), req)
	})

	// Archive it, then reuse the name for a new active template.
	req := withURLParam(newRequest(http.MethodPost, "/api/issue-templates/"+id+"/archive?workspace_id="+testWorkspaceID, nil), "id", id)
	testHandler.ArchiveIssueTemplate(httptest.NewRecorder(), req)
	reusedID := createIssueTemplateForTest(t, "Name reuse")
	t.Cleanup(func() {
		req := withURLParam(newRequest(http.MethodDelete, "/api/issue-templates/"+reusedID+"?workspace_id="+testWorkspaceID, nil), "id", reusedID)
		testHandler.DeleteIssueTemplate(httptest.NewRecorder(), req)
	})

	// Unarchiving the archived template must conflict on the active-name
	// uniqueness.
	w := httptest.NewRecorder()
	req = withURLParam(newRequest(http.MethodPost, "/api/issue-templates/"+id+"/unarchive?workspace_id="+testWorkspaceID, nil), "id", id)
	testHandler.UnarchiveIssueTemplate(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("unarchive with reused name status = %d body=%s (want 409)", w.Code, w.Body.String())
	}
}

func TestIssueTemplateWorkspaceIsolation(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	// A template created in another workspace must be unresolvable here.
	var otherWorkspaceID, otherUserID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO "user" (name, email)
		VALUES ($1, $2)
		RETURNING id
	`, "Isolation User", "isolation-"+handlerTestEmail).Scan(&otherUserID); err != nil {
		t.Fatal(err)
	}
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO workspace (name, slug, description, issue_prefix)
		VALUES ($1, $2, $3, $4)
		RETURNING id
	`, "Isolation Workspace", "handler-isolation", "Isolation workspace", "ISO").Scan(&otherWorkspaceID); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO member (workspace_id, user_id, role)
		VALUES ($1, $2, 'owner')
	`, otherWorkspaceID, otherUserID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, otherWorkspaceID); err != nil {
			t.Logf("cleanup workspace: %v", err)
		}
		if _, err := testPool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1`, otherUserID); err != nil {
			t.Logf("cleanup user: %v", err)
		}
	})

	var otherTemplateID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO issue_template (workspace_id, name, issue_title, created_by)
		VALUES ($1, $2, $3, $4)
		RETURNING id::text
	`, otherWorkspaceID, "Other workspace template", "Other", otherUserID).Scan(&otherTemplateID); err != nil {
		t.Fatal(err)
	}

	// Reading it through this workspace's handler must 404.
	w := httptest.NewRecorder()
	req := withURLParam(newRequest(http.MethodGet, "/api/issue-templates/"+otherTemplateID+"?workspace_id="+testWorkspaceID, nil), "id", otherTemplateID)
	testHandler.GetIssueTemplate(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("cross-workspace GetIssueTemplate status = %d (want 404)", w.Code)
	}

	// Archiving it through this workspace must 404 as well.
	w = httptest.NewRecorder()
	req = withURLParam(newRequest(http.MethodPost, "/api/issue-templates/"+otherTemplateID+"/archive?workspace_id="+testWorkspaceID, nil), "id", otherTemplateID)
	testHandler.ArchiveIssueTemplate(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("cross-workspace ArchiveIssueTemplate status = %d (want 404)", w.Code)
	}

	// And it must not appear in this workspace's active or include_archived list.
	w = httptest.NewRecorder()
	testHandler.ListIssueTemplates(w, newRequest(http.MethodGet, "/api/issue-templates?workspace_id="+testWorkspaceID+"&include_archived=true", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("ListIssueTemplates status = %d", w.Code)
	}
	var list []map[string]any
	if err := json.NewDecoder(w.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	for _, item := range list {
		if item["id"] == otherTemplateID {
			t.Fatalf("cross-workspace template leaked into list: %#v", item)
		}
	}
}

func TestIssueTemplateCRUD(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	createReq := map[string]any{
		"name":          "Bug report",
		"issue_title":   "Investigate {{area}} bug",
		"issue_content": "## Context\n\n## Steps\n",
	}
	w := httptest.NewRecorder()
	testHandler.CreateIssueTemplate(w, newRequest(http.MethodPost, "/api/issue-templates?workspace_id="+testWorkspaceID, createReq))
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssueTemplate status = %d body=%s", w.Code, w.Body.String())
	}

	var created map[string]any
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatalf("created response missing id: %#v", created)
	}
	if created["name"] != "Bug report" || created["issue_title"] != "Investigate {{area}} bug" {
		t.Fatalf("created response mismatch: %#v", created)
	}
	t.Cleanup(func() {
		req := withURLParam(newRequest(http.MethodDelete, "/api/issue-templates/"+id+"?workspace_id="+testWorkspaceID, nil), "id", id)
		testHandler.DeleteIssueTemplate(httptest.NewRecorder(), req)
	})

	w = httptest.NewRecorder()
	testHandler.ListIssueTemplates(w, newRequest(http.MethodGet, "/api/issue-templates?workspace_id="+testWorkspaceID, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("ListIssueTemplates status = %d body=%s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	req := withURLParam(newRequest(http.MethodGet, "/api/issue-templates/"+id+"?workspace_id="+testWorkspaceID, nil), "id", id)
	testHandler.GetIssueTemplate(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GetIssueTemplate status = %d body=%s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	req = withURLParam(newRequest(http.MethodPut, "/api/issue-templates/"+id+"?workspace_id="+testWorkspaceID, map[string]any{
		"name":          "Bug triage",
		"issue_title":   "Triage bug",
		"issue_content": "Updated body",
	}), "id", id)
	testHandler.UpdateIssueTemplate(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateIssueTemplate status = %d body=%s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	req = withURLParam(newRequest(http.MethodDelete, "/api/issue-templates/"+id+"?workspace_id="+testWorkspaceID, nil), "id", id)
	testHandler.DeleteIssueTemplate(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("DeleteIssueTemplate status = %d body=%s", w.Code, w.Body.String())
	}
}

func TestIssueTemplateListOmitsContent(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	createReq := map[string]any{
		"name":          "Content test template",
		"issue_title":   "Title here",
		"issue_content": "## Long body that should not appear in list",
	}
	w := httptest.NewRecorder()
	testHandler.CreateIssueTemplate(w, newRequest(http.MethodPost, "/api/issue-templates?workspace_id="+testWorkspaceID, createReq))
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssueTemplate status = %d body=%s", w.Code, w.Body.String())
	}
	var created map[string]any
	json.NewDecoder(w.Body).Decode(&created)
	id := created["id"].(string)
	t.Cleanup(func() {
		req := withURLParam(newRequest(http.MethodDelete, "/api/issue-templates/"+id+"?workspace_id="+testWorkspaceID, nil), "id", id)
		testHandler.DeleteIssueTemplate(httptest.NewRecorder(), req)
	})

	w = httptest.NewRecorder()
	testHandler.ListIssueTemplates(w, newRequest(http.MethodGet, "/api/issue-templates?workspace_id="+testWorkspaceID, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("ListIssueTemplates status = %d", w.Code)
	}
	var list []map[string]any
	json.NewDecoder(w.Body).Decode(&list)
	for _, item := range list {
		if _, hasContent := item["issue_content"]; hasContent {
			t.Fatalf("list response should not contain issue_content, got: %v", item)
		}
	}

	w = httptest.NewRecorder()
	req := withURLParam(newRequest(http.MethodGet, "/api/issue-templates/"+id+"?workspace_id="+testWorkspaceID, nil), "id", id)
	testHandler.GetIssueTemplate(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GetIssueTemplate status = %d", w.Code)
	}
	var detail map[string]any
	json.NewDecoder(w.Body).Decode(&detail)
	if detail["issue_content"] != "## Long body that should not appear in list" {
		t.Fatalf("detail should contain issue_content, got: %v", detail["issue_content"])
	}
}

func TestIssueTemplateValidation(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	tests := []struct {
		name string
		body map[string]any
	}{
		{
			name: "missing name",
			body: map[string]any{
				"issue_title":   "Title",
				"issue_content": "Content",
			},
		},
		{
			name: "missing issue title",
			body: map[string]any{
				"name":          "Template",
				"issue_content": "Content",
			},
		},
		{
			name: "config is array",
			body: map[string]any{
				"name":        "Template",
				"issue_title": "Title",
				"config":      []any{"a", "b"},
			},
		},
		{
			name: "config is scalar",
			body: map[string]any{
				"name":        "Template",
				"issue_title": "Title",
				"config":      "invalid",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			testHandler.CreateIssueTemplate(w, newRequest(http.MethodPost, "/api/issue-templates?workspace_id="+testWorkspaceID, tt.body))
			if w.Code != http.StatusBadRequest {
				t.Fatalf("CreateIssueTemplate status = %d body=%s", w.Code, w.Body.String())
			}
		})
	}
}
