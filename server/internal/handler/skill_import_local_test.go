package handler

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestImportLocalSkillsCreatesValidSkillAndFiles(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	name := "Uploaded Review " + time.Now().Format("150405.000000000")
	w := httptest.NewRecorder()
	req := newRequestAsUser(testUserID, http.MethodPost, "/api/skills/import-local", map[string]any{
		"skills": []map[string]any{{
			"name":        name,
			"description": "Reviews pull requests",
			"content":     "# Uploaded Review",
			"files": []map[string]any{{
				"path":    "templates/review.md",
				"content": "review body",
			}},
			"source": map[string]any{
				"type":  "uploaded_bundle",
				"label": "team.zip/uploaded-review",
			},
		}},
	})

	testHandler.ImportLocalSkills(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp ImportLocalSkillsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Created) != 1 || resp.Created[0].Skill.Name != name {
		t.Fatalf("created = %#v", resp.Created)
	}
	if len(resp.Created[0].Skill.Files) != 1 || resp.Created[0].Skill.Files[0].Path != "templates/review.md" {
		t.Fatalf("files = %#v", resp.Created[0].Skill.Files)
	}
}

func TestImportLocalSkillsSkipsDuplicateAndContinues(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	name := "Duplicate Upload " + time.Now().Format("150405.000000000")
	createSkillForLocalImportTest(t, name)
	w := httptest.NewRecorder()
	req := newRequestAsUser(testUserID, http.MethodPost, "/api/skills/import-local", map[string]any{
		"skills": []map[string]any{
			{"name": name, "content": "# Duplicate"},
			{"name": name + " Fresh", "content": "# Fresh"},
		},
	})

	testHandler.ImportLocalSkills(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp ImportLocalSkillsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Created) != 1 || resp.Created[0].Skill.Name != name+" Fresh" {
		t.Fatalf("created = %#v", resp.Created)
	}
	if len(resp.Skipped) != 1 || resp.Skipped[0].Reason != "already_exists" {
		t.Fatalf("skipped = %#v", resp.Skipped)
	}
}

func TestImportLocalSkillsRejectsInvalidPathAndLimits(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	w := httptest.NewRecorder()
	req := newRequestAsUser(testUserID, http.MethodPost, "/api/skills/import-local", map[string]any{
		"skills": []map[string]any{{
			"name":    "Bad Path " + time.Now().Format("150405.000000000"),
			"content": "# Bad",
			"files": []map[string]any{{
				"path":    "../secret.md",
				"content": "no",
			}},
		}},
	})

	testHandler.ImportLocalSkills(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected structured 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp ImportLocalSkillsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Failed) != 1 || resp.Failed[0].Reason != "invalid_file_path" {
		t.Fatalf("failed = %#v", resp.Failed)
	}
}

func TestImportLocalSkillsRejectsSingleOversizedSupportingFile(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	w := httptest.NewRecorder()
	req := newRequestAsUser(testUserID, http.MethodPost, "/api/skills/import-local", map[string]any{
		"skills": []map[string]any{{
			"name":    "Large File " + time.Now().Format("150405.000000000"),
			"content": "# Large File",
			"files": []map[string]any{{
				"path":    "templates/large.md",
				"content": strings.Repeat("a", 2*1024*1024),
			}},
		}},
	})

	testHandler.ImportLocalSkills(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected structured 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp ImportLocalSkillsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Failed) != 1 || resp.Failed[0].Reason != "file_too_large" {
		t.Fatalf("failed = %#v", resp.Failed)
	}
}

func TestImportLocalSkillsRejectsOversizedPrimarySkillFile(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	w := httptest.NewRecorder()
	req := newRequestAsUser(testUserID, http.MethodPost, "/api/skills/import-local", map[string]any{
		"skills": []map[string]any{{
			"name":    "Large Skill " + time.Now().Format("150405.000000000"),
			"content": strings.Repeat("a", 2*1024*1024),
		}},
	})

	testHandler.ImportLocalSkills(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected structured 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp ImportLocalSkillsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Failed) != 1 || resp.Failed[0].Reason != "file_too_large" {
		t.Fatalf("failed = %#v", resp.Failed)
	}
}

func TestImportLocalSkillsRejectsCleanedSkillMDPath(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	w := httptest.NewRecorder()
	req := newRequestAsUser(testUserID, http.MethodPost, "/api/skills/import-local", map[string]any{
		"skills": []map[string]any{{
			"name":    "Bad Clean Path " + time.Now().Format("150405.000000000"),
			"content": "# Bad",
			"files": []map[string]any{{
				"path":    "templates/../SKILL.md",
				"content": "would overwrite the primary skill file",
			}},
		}},
	})

	testHandler.ImportLocalSkills(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected structured 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp ImportLocalSkillsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Created) != 0 {
		t.Fatalf("created = %#v", resp.Created)
	}
	if len(resp.Failed) != 1 || resp.Failed[0].Reason != "invalid_file_path" {
		t.Fatalf("failed = %#v", resp.Failed)
	}
}

func TestImportLocalSkillsRejectsOversizedRequestBodyBeforeDecode(t *testing.T) {
	w := httptest.NewRecorder()
	body := io.MultiReader(
		strings.NewReader(`{"skills":[{"name":"Too Large","content":"`),
		io.LimitReader(repeatedByteReader('a'), 65),
		strings.NewReader(`"}]}`),
	)
	req := httptest.NewRequest(http.MethodPost, "/api/skills/import-local", body)
	req.Header.Set("Content-Type", "application/json")

	if _, ok := decodeImportLocalSkillsRequest(w, req, 64); ok {
		t.Fatal("expected oversized request body to fail before decode")
	}

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for oversized request body, got %d: %s", w.Code, w.Body.String())
	}
}

func TestImportLocalSkillRequestCapStaysBelowHundredsOfMiB(t *testing.T) {
	maxCap := 32 * 1024 * 1024
	if maxLocalUploadedSkillRequestBytes > maxCap {
		t.Fatalf("request cap = %d, want at most %d", maxLocalUploadedSkillRequestBytes, maxCap)
	}
}

func TestImportLocalSkillsRejectsHugeDecodedPayloadBeforeDecode(t *testing.T) {
	w := httptest.NewRecorder()
	body := io.MultiReader(
		strings.NewReader(`{"skills":[{"name":"Too Large","content":"`),
		io.LimitReader(repeatedByteReader('a'), int64(maxLocalUploadedSkillRequestBytes+1)),
		strings.NewReader(`"}]}`),
	)
	req := httptest.NewRequest(http.MethodPost, "/api/skills/import-local", body)
	req.Header.Set("Content-Type", "application/json")

	if _, ok := decodeImportLocalSkillsRequest(w, req, int64(maxLocalUploadedSkillRequestBytes)); ok {
		t.Fatal("expected huge decoded payload to fail before decode")
	}

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for huge decoded payload, got %d: %s", w.Code, w.Body.String())
	}
}

func TestImportLocalSkillsRejectsTooManySkills(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	skills := []map[string]any{}
	for i := 0; i < maxLocalUploadedSkills+1; i++ {
		skills = append(skills, map[string]any{
			"name":    "Batch Item " + time.Now().Format("150405.000000000") + "-" + string(rune('A'+i)),
			"content": "# Batch Item",
		})
	}

	w := httptest.NewRecorder()
	req := newRequestAsUser(testUserID, http.MethodPost, "/api/skills/import-local", map[string]any{
		"skills": skills,
	})

	testHandler.ImportLocalSkills(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for too many skills, got %d: %s", w.Code, w.Body.String())
	}
}

func TestImportLocalSkillsAllowsEscapedJSONWithinDecodedBundleLimit(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	w := httptest.NewRecorder()
	req := newRequestAsUser(testUserID, http.MethodPost, "/api/skills/import-local", map[string]any{
		"skills": []map[string]any{{
			"name":    "Escaped Bundle " + time.Now().Format("150405.000000000"),
			"content": "# Escaped",
			"files": []map[string]any{
				{"path": "templates/quotes-1.md", "content": strings.Repeat(`"`, 900*1024)},
				{"path": "templates/quotes-2.md", "content": strings.Repeat(`"`, 900*1024)},
				{"path": "templates/quotes-3.md", "content": strings.Repeat(`"`, 900*1024)},
				{"path": "templates/quotes-4.md", "content": strings.Repeat(`"`, 900*1024)},
				{"path": "templates/quotes-5.md", "content": strings.Repeat(`"`, 900*1024)},
			},
		}},
	})

	testHandler.ImportLocalSkills(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for escaped JSON under decoded limit, got %d: %s", w.Code, w.Body.String())
	}
	var resp ImportLocalSkillsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Created) != 1 {
		t.Fatalf("created = %#v failed = %#v", resp.Created, resp.Failed)
	}
}

func TestImportLocalSkillsAllowsEscapedJSONForMultipleDecodedBundles(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	skills := []map[string]any{}
	for i := 0; i < 3; i++ {
		skills = append(skills, map[string]any{
			"name":    "Escaped Batch " + time.Now().Format("150405.000000000") + "-" + string(rune('A'+i)),
			"content": "# Escaped Batch",
			"files": []map[string]any{
				{"path": "templates/quotes-1.md", "content": strings.Repeat(`"`, 900*1024)},
				{"path": "templates/quotes-2.md", "content": strings.Repeat(`"`, 900*1024)},
				{"path": "templates/quotes-3.md", "content": strings.Repeat(`"`, 900*1024)},
				{"path": "templates/quotes-4.md", "content": strings.Repeat(`"`, 900*1024)},
				{"path": "templates/quotes-5.md", "content": strings.Repeat(`"`, 900*1024)},
			},
		})
	}

	w := httptest.NewRecorder()
	req := newRequestAsUser(testUserID, http.MethodPost, "/api/skills/import-local", map[string]any{
		"skills": skills,
	})

	testHandler.ImportLocalSkills(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for escaped JSON batch under decoded limit, got %d: %s", w.Code, w.Body.String())
	}
	var resp ImportLocalSkillsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Created) != 3 {
		t.Fatalf("created = %#v failed = %#v", resp.Created, resp.Failed)
	}
}

func TestImportLocalSkillsRejectsHiddenFilePaths(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	w := httptest.NewRecorder()
	req := newRequestAsUser(testUserID, http.MethodPost, "/api/skills/import-local", map[string]any{
		"skills": []map[string]any{{
			"name":    "Hidden File " + time.Now().Format("150405.000000000"),
			"content": "# Hidden File",
			"files": []map[string]any{
				{"path": ".env", "content": "SECRET=foo"},
				{"path": "templates/.DS_Store", "content": "binary"},
			},
		}},
	})

	testHandler.ImportLocalSkills(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected structured 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp ImportLocalSkillsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Failed) != 1 || resp.Failed[0].Reason != "invalid_file_path" {
		t.Fatalf("failed = %#v", resp.Failed)
	}
}

func TestImportLocalSkillsRejectsMetadataFilePaths(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	w := httptest.NewRecorder()
	req := newRequestAsUser(testUserID, http.MethodPost, "/api/skills/import-local", map[string]any{
		"skills": []map[string]any{{
			"name":    "Metadata File " + time.Now().Format("150405.000000000"),
			"content": "# Metadata File",
			"files": []map[string]any{
				{"path": "Thumbs.db", "content": "binary"},
			},
		}},
	})

	testHandler.ImportLocalSkills(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected structured 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp ImportLocalSkillsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Failed) != 1 || resp.Failed[0].Reason != "invalid_file_path" {
		t.Fatalf("failed = %#v", resp.Failed)
	}
}

func TestNormalizeLocalUploadedSkillFilePathRejectsBackslashTraversal(t *testing.T) {
	if _, ok := normalizeLocalUploadedSkillFilePath(`templates\..\SKILL.md`); ok {
		t.Fatal("expected Windows-style traversal to be rejected")
	}
}

type repeatedByteReader byte

func (r repeatedByteReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = byte(r)
	}
	return len(p), nil
}

func createSkillForLocalImportTest(t *testing.T, name string) {
	t.Helper()

	w := httptest.NewRecorder()
	req := newRequest(http.MethodPost, "/api/skills", map[string]any{
		"name":    name,
		"content": "# Existing",
	})
	testHandler.CreateSkill(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create existing skill: expected 201, got %d: %s", w.Code, w.Body.String())
	}
}
