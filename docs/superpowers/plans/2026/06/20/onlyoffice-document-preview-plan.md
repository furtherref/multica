# OnlyOffice Document Preview Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add read-only in-app preview for office document attachments (Word/Excel/PPT + ODF + CSV) by embedding the deployed OnlyOffice Document Server's viewer on web + desktop.

**Architecture:** A user clicks the Eye button on an office attachment → the frontend fetches a JWT-signed editor config from a new authed backend endpoint → it loads the OnlyOffice `api.js` from the public Document Server and renders the viewer (`mode: "view"`) in the existing preview modal. The Document Server fetches the file bytes server-to-server from a second, public, HMAC-token-authorized backend endpoint.

**Tech Stack:** Go (chi, golang-jwt/jwt/v5, crypto/hmac), TypeScript (React, TanStack Query, zod), OnlyOffice DocsAPI, Helm.

**Design spec:** `docs/superpowers/specs/2026/06/20/onlyoffice-document-preview-design.md`

## Global Constraints

- TypeScript strict mode; explicit types. Comments in code are English only.
- **API Response Compatibility:** parse, don't cast. Every response consumed by UI runs through a `zod` schema via `parseWithFallback` (`packages/core/api/schema.ts`). Add a test that feeds a malformed response (missing field / null body) through the schema.
- **Backend UUID convention:** the office-config resource param is resolved through the existing `loadAttachmentForRequest` loader; the public content endpoint validates its UUID with `util.ParseUUID(s) (pgtype.UUID, error)` and checks the error. Never round-trip raw user strings into write queries (none here — both endpoints are read-only).
- **Package boundaries:** the office preview component lives in `packages/views/` (no `next/*`, no `react-router-dom`, no stores). It may use `window`/`document` (it renders only in the browser). `packages/core/` stays free of react-dom.
- **Locked design decisions:** read-only (`mode: "view"`, no callback endpoint); CSV → office (`documentType: "cell"`), office detection takes priority over text; `document.url` points at the backend **public** URL (the DS blocks private IPs); the fetch token uses a dedicated `ONLYOFFICE_FETCH_SECRET`; `permissions.print = true`.
- Conventional commits: `feat(scope)`, `test(scope)`, `chore(scope)`.

---

## File Structure

**Backend (Go)**
- Create: `server/internal/handler/office.go` — both handlers + pure helpers (`officeDocType`, `officeFetchToken`, `verifyOfficeFetchToken`, `signOfficeConfig`).
- Create: `server/internal/handler/office_test.go` — pure-helper + handler tests.
- Modify: `server/internal/handler/handler.go:51-87` — add OnlyOffice fields to `Config`.
- Modify: `server/cmd/server/router.go:145-156` — read OnlyOffice env vars into `Config`.
- Modify: `server/cmd/server/router.go` — register the two new routes.

**Frontend (TS)**
- Modify: `packages/views/editor/utils/preview.ts` — add `"office"` kind + detection; drop `csv` from the text set.
- Create: `packages/views/editor/utils/preview.test.ts` — office-detection tests.
- Create: `packages/core/types/office.ts` — `OfficeConfig` type.
- Modify: `packages/core/types/index.ts` — re-export `./office`.
- Modify: `packages/core/api/schemas.ts` — `OfficeConfigResponseSchema` + `EMPTY_OFFICE_CONFIG`.
- Create: `packages/core/api/office-schema.test.ts` — schema fallback tests.
- Modify: `packages/core/api/client.ts` — `getOfficeConfig(id)` method.
- Create: `packages/views/editor/utils/docs-api-loader.ts` — one-shot `api.js` loader + DocsAPI types.
- Create: `packages/views/editor/utils/docs-api-loader.test.ts` — loader dedup/error tests.
- Create: `packages/views/editor/attachment-preview-fallback.tsx` — `UnsupportedFallback` extracted for reuse (avoids a circular import).
- Create: `packages/views/editor/office-attachment-preview.tsx` — the OnlyOffice viewer component.
- Create: `packages/views/editor/office-attachment-preview.test.tsx` — component lifecycle test.
- Modify: `packages/views/editor/attachment-preview-modal.tsx` — import the extracted fallback; add the `office` dispatch case.
- Create: `packages/views/editor/office-attachment-preview.modal.test.tsx` — modal wiring test.

**Infra (Helm)**
- Modify: `deploy/helm/multica/values.yaml` — three new `backend.config` keys.
- Modify: `deploy/helm/multica/templates/configmap.yaml` — three new ConfigMap entries.

---

## Task B1: Office config fields + type detection

**Files:**
- Create: `server/internal/handler/office.go`
- Create: `server/internal/handler/office_test.go`
- Modify: `server/internal/handler/handler.go:51-87`
- Modify: `server/cmd/server/router.go:145-156`

**Interfaces:**
- Produces: `officeDocType(filename string) (docType, fileType string, ok bool)`; `Config.OnlyOfficeEnabled`, `Config.OnlyOfficeDocumentServerPublicURL`, `Config.OnlyOfficeJWTSecret`, `Config.OnlyOfficeFetchSecret`, `Config.OnlyOfficeFetchBaseURL`.

- [ ] **Step 1: Write the failing test**

Create `server/internal/handler/office_test.go`:

```go
package handler

import "testing"

func TestOfficeDocType(t *testing.T) {
	cases := []struct {
		filename     string
		wantDocType  string
		wantFileType string
		wantOK       bool
	}{
		{"Report.docx", "word", "docx", true},
		{"legacy.doc", "word", "doc", true},
		{"notes.odt", "word", "odt", true},
		{"data.xlsx", "cell", "xlsx", true},
		{"data.csv", "cell", "csv", true},
		{"deck.pptx", "slide", "pptx", true},
		{"slides.odp", "slide", "odp", true},
		{"photo.png", "", "", false},
		{"readme.md", "", "", false},
		{"NoExtension", "", "", false},
	}
	for _, c := range cases {
		gotDoc, gotFile, gotOK := officeDocType(c.filename)
		if gotDoc != c.wantDocType || gotFile != c.wantFileType || gotOK != c.wantOK {
			t.Errorf("officeDocType(%q) = (%q,%q,%v), want (%q,%q,%v)",
				c.filename, gotDoc, gotFile, gotOK, c.wantDocType, c.wantFileType, c.wantOK)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd server && go test ./internal/handler/ -run TestOfficeDocType`
Expected: FAIL — `undefined: officeDocType`.

- [ ] **Step 3: Write minimal implementation**

Create `server/internal/handler/office.go`:

```go
package handler

import (
	"path"
	"strings"
)

// officeDocType maps a filename to OnlyOffice's (documentType, fileType).
// documentType is one of "word" | "cell" | "slide". fileType is the bare
// extension OnlyOffice expects (e.g. "docx"). ok is false for non-office
// files. CSV is intentionally treated as a "cell" document (design decision).
func officeDocType(filename string) (docType, fileType string, ok bool) {
	ext := strings.ToLower(strings.TrimPrefix(path.Ext(filename), "."))
	switch ext {
	case "doc", "docx", "odt", "rtf":
		return "word", ext, true
	case "xls", "xlsx", "ods", "csv":
		return "cell", ext, true
	case "ppt", "pptx", "odp":
		return "slide", ext, true
	default:
		return "", "", false
	}
}
```

- [ ] **Step 4: Add the Config fields**

In `server/internal/handler/handler.go`, inside `type Config struct { ... }` (ends at line 87, after `AttachmentDownloadURLTTL time.Duration`), add:

```go
	// OnlyOffice read-only document preview. When OnlyOfficeEnabled is false
	// the office-config endpoint 404s and the frontend falls back to a plain
	// download card.
	OnlyOfficeEnabled                 bool
	OnlyOfficeDocumentServerPublicURL string
	// OnlyOfficeJWTSecret must equal the Document Server's JWT_SECRET; used to
	// sign the browser editor config.
	OnlyOfficeJWTSecret string
	// OnlyOfficeFetchSecret signs the short-lived token the Document Server
	// presents when fetching the file. Independent of OnlyOfficeJWTSecret.
	OnlyOfficeFetchSecret string
	// OnlyOfficeFetchBaseURL is the base of document.url that the Document
	// Server fetches. MUST be a public, non-private-IP host: the DS blocks
	// private/reserved addresses by default. Falls back to PublicURL.
	OnlyOfficeFetchBaseURL string
```

- [ ] **Step 5: Wire the env vars**

In `server/cmd/server/router.go`, inside the `signupConfig := handler.Config{ ... }` literal (lines 145-156), add after `AttachmentDownloadURLTTL: ...`:

```go
		OnlyOfficeEnabled:                 os.Getenv("ONLYOFFICE_ENABLED") == "true",
		OnlyOfficeDocumentServerPublicURL: strings.TrimRight(strings.TrimSpace(os.Getenv("ONLYOFFICE_DOCUMENT_SERVER_PUBLIC_URL")), "/"),
		OnlyOfficeJWTSecret:               os.Getenv("ONLYOFFICE_JWT_SECRET"),
		OnlyOfficeFetchSecret:             os.Getenv("ONLYOFFICE_FETCH_SECRET"),
		OnlyOfficeFetchBaseURL:            strings.TrimRight(strings.TrimSpace(os.Getenv("ONLYOFFICE_FETCH_BASE_URL")), "/"),
```

(`os` and `strings` are already imported in router.go.)

- [ ] **Step 6: Run test to verify it passes**

Run: `cd server && go test ./internal/handler/ -run TestOfficeDocType && go build ./...`
Expected: PASS, and the build succeeds (Config fields + env wiring compile).

- [ ] **Step 7: Commit**

```bash
git add server/internal/handler/office.go server/internal/handler/office_test.go server/internal/handler/handler.go server/cmd/server/router.go
git commit -m "feat(office): add office type detection and OnlyOffice config fields"
```

---

## Task B2: Fetch-token mint & verify (HMAC)

**Files:**
- Modify: `server/internal/handler/office.go`
- Modify: `server/internal/handler/office_test.go`

**Interfaces:**
- Produces: `officeFetchToken(attachmentID, secret string, exp time.Time) string`; `verifyOfficeFetchToken(token, attachmentID, secret string, now time.Time) bool`.

- [ ] **Step 1: Write the failing test**

Append to `server/internal/handler/office_test.go`:

```go
func TestOfficeFetchTokenRoundTrip(t *testing.T) {
	const id, secret = "att-123", "s3cr3t"
	now := time.Unix(1_700_000_000, 0)
	tok := officeFetchToken(id, secret, now.Add(5*time.Minute))

	if !verifyOfficeFetchToken(tok, id, secret, now) {
		t.Fatal("fresh token should verify")
	}
	if verifyOfficeFetchToken(tok, id, secret, now.Add(6*time.Minute)) {
		t.Error("expired token must not verify")
	}
	if verifyOfficeFetchToken(tok, "other-id", secret, now) {
		t.Error("token bound to a different attachment must not verify")
	}
	if verifyOfficeFetchToken(tok, id, "wrong-secret", now) {
		t.Error("token under a different secret must not verify")
	}
	if verifyOfficeFetchToken("garbage", id, secret, now) {
		t.Error("malformed token must not verify")
	}
}
```

Add `"time"` to the test file's imports.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd server && go test ./internal/handler/ -run TestOfficeFetchTokenRoundTrip`
Expected: FAIL — `undefined: officeFetchToken`.

- [ ] **Step 3: Write minimal implementation**

In `server/internal/handler/office.go`, extend the import block and add the helpers:

```go
import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"path"
	"strconv"
	"strings"
	"time"
)
```

```go
// officeFetchToken mints an opaque token authorizing the Document Server to
// fetch exactly one attachment until exp. Format: "<expUnix>.<hexHMAC>" where
// HMAC = HMAC-SHA256(secret, attachmentID + "." + expUnix).
func officeFetchToken(attachmentID, secret string, exp time.Time) string {
	expStr := strconv.FormatInt(exp.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(attachmentID + "." + expStr))
	return expStr + "." + hex.EncodeToString(mac.Sum(nil))
}

// verifyOfficeFetchToken reports whether token is a well-formed, unexpired
// HMAC for attachmentID under secret. now is injected for testability.
func verifyOfficeFetchToken(token, attachmentID, secret string, now time.Time) bool {
	dot := strings.IndexByte(token, '.')
	if dot <= 0 {
		return false
	}
	expStr, sig := token[:dot], token[dot+1:]
	expUnix, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil || now.Unix() > expUnix {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(attachmentID + "." + expStr))
	want := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(want), []byte(sig))
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd server && go test ./internal/handler/ -run 'TestOfficeFetchTokenRoundTrip|TestOfficeDocType'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add server/internal/handler/office.go server/internal/handler/office_test.go
git commit -m "feat(office): add HMAC fetch-token mint and verify"
```

---

## Task B3: Editor config JWT signing

**Files:**
- Modify: `server/internal/handler/office.go`
- Modify: `server/internal/handler/office_test.go`

**Interfaces:**
- Produces: `signOfficeConfig(config map[string]any, secret string) (string, error)`.

- [ ] **Step 1: Write the failing test**

Append to `server/internal/handler/office_test.go` (add `"github.com/golang-jwt/jwt/v5"` to imports):

```go
func TestSignOfficeConfig(t *testing.T) {
	const secret = "jwt-secret"
	config := map[string]any{
		"documentType": "word",
		"document":     map[string]any{"fileType": "docx", "key": "abc"},
	}
	signed, err := signOfficeConfig(config, secret)
	if err != nil {
		t.Fatalf("signOfficeConfig: %v", err)
	}
	if signed == "" {
		t.Fatal("expected a non-empty JWT")
	}

	parsed, err := jwt.Parse(signed, func(token *jwt.Token) (any, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, jwt.ErrSignatureInvalid
		}
		return []byte(secret), nil
	})
	if err != nil || !parsed.Valid {
		t.Fatalf("signed config must verify under the same secret: err=%v", err)
	}
	claims := parsed.Claims.(jwt.MapClaims)
	if claims["documentType"] != "word" {
		t.Errorf("documentType claim = %v, want word", claims["documentType"])
	}

	// Wrong secret must not verify.
	if _, err := jwt.Parse(signed, func(*jwt.Token) (any, error) { return []byte("nope"), nil }); err == nil {
		t.Error("JWT verified under the wrong secret")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd server && go test ./internal/handler/ -run TestSignOfficeConfig`
Expected: FAIL — `undefined: signOfficeConfig`.

- [ ] **Step 3: Write minimal implementation**

Add `"github.com/golang-jwt/jwt/v5"` to `office.go`'s imports and add:

```go
// signOfficeConfig returns the HS256 JWT of the config payload. The config map
// passed in MUST NOT already contain a "token" field — OnlyOffice 7.1+ rejects
// a config whose embedded token signs over itself. Callers build the token-less
// config, call this, then assign the result to config["token"].
func signOfficeConfig(config map[string]any, secret string) (string, error) {
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims(config))
	return tok.SignedString([]byte(secret))
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd server && go test ./internal/handler/ -run TestSignOfficeConfig`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add server/internal/handler/office.go server/internal/handler/office_test.go
git commit -m "feat(office): add HS256 editor-config signing"
```

---

## Task B4: `GET /api/attachments/{id}/office-config` handler + route

**Files:**
- Modify: `server/internal/handler/office.go`
- Modify: `server/internal/handler/office_test.go`
- Modify: `server/cmd/server/router.go` (authed workspace-member group, near line 830)

**Interfaces:**
- Consumes: `officeDocType`, `officeFetchToken`, `signOfficeConfig`; `h.loadAttachmentForRequest`; `h.cfg.*`; `uuidToString`, `writeJSON`, `writeError`.
- Produces: `func (h *Handler) GetOfficeConfig(w http.ResponseWriter, r *http.Request)`.

- [ ] **Step 1: Write the failing test**

Append to `server/internal/handler/office_test.go` (add imports `context`, `encoding/json`, `net/http`, `net/http/httptest`, `strings`). Add a seed helper and the test:

```go
func seedOfficeAttachment(t *testing.T, filename, contentType string) string {
	t.Helper()
	var id string
	err := testPool.QueryRow(context.Background(), `
		INSERT INTO attachment (workspace_id, uploader_type, uploader_id, filename, url, content_type, size_bytes)
		VALUES ($1, 'member', $2, $3, $4, $5, $6)
		RETURNING id
	`, testWorkspaceID, testUserID, filename,
		"https://cdn.example.com/workspaces/"+testWorkspaceID+"/"+filename,
		contentType, int64(1234)).Scan(&id)
	if err != nil {
		t.Fatalf("seed attachment: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM attachment WHERE id = $1`, id)
	})
	return id
}

func TestGetOfficeConfig(t *testing.T) {
	prev := testHandler.cfg
	testHandler.cfg.OnlyOfficeEnabled = true
	testHandler.cfg.OnlyOfficeDocumentServerPublicURL = "https://weboffice.example.com"
	testHandler.cfg.OnlyOfficeJWTSecret = "jwt-secret"
	testHandler.cfg.OnlyOfficeFetchSecret = "fetch-secret"
	testHandler.cfg.OnlyOfficeFetchBaseURL = "https://api.example.com"
	t.Cleanup(func() { testHandler.cfg = prev })

	id := seedOfficeAttachment(t, "Report.docx",
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document")

	req := newRequest("GET", "/api/attachments/"+id+"/office-config", nil)
	req = withURLParam(req, "id", id)
	w := httptest.NewRecorder()
	testHandler.GetOfficeConfig(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		DocumentServerURL string         `json:"document_server_url"`
		Config            map[string]any `json:"config"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.DocumentServerURL != "https://weboffice.example.com" {
		t.Errorf("document_server_url = %q", resp.DocumentServerURL)
	}
	if resp.Config["documentType"] != "word" {
		t.Errorf("documentType = %v", resp.Config["documentType"])
	}
	doc := resp.Config["document"].(map[string]any)
	url, _ := doc["url"].(string)
	if !strings.HasPrefix(url, "https://api.example.com/api/office/"+id+"/content?token=") {
		t.Errorf("document.url = %q", url)
	}
	if tok, _ := resp.Config["token"].(string); tok == "" {
		t.Error("config.token must be a non-empty signed JWT")
	}

	// Non-office attachment → 400.
	pngID := seedOfficeAttachment(t, "photo.png", "image/png")
	req2 := withURLParam(newRequest("GET", "/api/attachments/"+pngID+"/office-config", nil), "id", pngID)
	w2 := httptest.NewRecorder()
	testHandler.GetOfficeConfig(w2, req2)
	if w2.Code != http.StatusBadRequest {
		t.Errorf("non-office: expected 400, got %d", w2.Code)
	}

	// Enabled but missing JWT secret → 503 (fail closed; never sign with "").
	testHandler.cfg.OnlyOfficeJWTSecret = ""
	reqMis := withURLParam(newRequest("GET", "/api/attachments/"+id+"/office-config", nil), "id", id)
	wMis := httptest.NewRecorder()
	testHandler.GetOfficeConfig(wMis, reqMis)
	if wMis.Code != http.StatusServiceUnavailable {
		t.Errorf("misconfigured: expected 503, got %d", wMis.Code)
	}
	testHandler.cfg.OnlyOfficeJWTSecret = "jwt-secret"

	// Disabled → 404.
	testHandler.cfg.OnlyOfficeEnabled = false
	req3 := withURLParam(newRequest("GET", "/api/attachments/"+id+"/office-config", nil), "id", id)
	w3 := httptest.NewRecorder()
	testHandler.GetOfficeConfig(w3, req3)
	if w3.Code != http.StatusNotFound {
		t.Errorf("disabled: expected 404, got %d", w3.Code)
	}
}
```

Add this chi URL-param helper to the test file (it injects the `{id}` route param that `chi.URLParam` reads, since the handler is called directly without the router):

```go
func withURLParam(r *http.Request, key, val string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(key, val)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}
```

Add `"github.com/go-chi/chi/v5"` to the test imports.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd server && go test ./internal/handler/ -run TestGetOfficeConfig`
Expected: FAIL — `undefined: GetOfficeConfig` (or DB-skipped; if skipped, see note below).

> **DB note:** `handler_test.go`'s `TestMain` skips the whole package when `DATABASE_URL` is unreachable. Ensure a test DB is up (`make db-up` and the migrations applied) so these handler tests actually run.

- [ ] **Step 3: Write minimal implementation**

In `server/internal/handler/office.go` add `"net/http"`, `"net/url"`, `"time"` to imports (time already added in B2) and append:

```go
const officeFetchTokenTTL = 5 * time.Minute

// officeSecretsReady reports that the secrets and fetch base needed to mint and
// validate tokens are present. Both endpoints fail closed on this: signing a
// config or HMAC with an empty secret would be forgeable, so "enabled but a
// secret is missing" must NOT silently produce signed-with-"" output.
func (h *Handler) officeSecretsReady() bool {
	base := h.cfg.OnlyOfficeFetchBaseURL
	if base == "" {
		base = h.cfg.PublicURL
	}
	return h.cfg.OnlyOfficeJWTSecret != "" &&
		h.cfg.OnlyOfficeFetchSecret != "" &&
		base != ""
}

// GetOfficeConfig returns a JWT-signed OnlyOffice editor config for an office
// attachment. Mounted inside the authenticated workspace-member group; the
// membership check is performed by that middleware, and loadAttachmentForRequest
// scopes the lookup to the request's workspace.
func (h *Handler) GetOfficeConfig(w http.ResponseWriter, r *http.Request) {
	if !h.cfg.OnlyOfficeEnabled {
		writeError(w, http.StatusNotFound, "office preview is not enabled")
		return
	}
	// Enabled but misconfigured → fail closed (503), never sign with "".
	if !h.officeSecretsReady() || h.cfg.OnlyOfficeDocumentServerPublicURL == "" {
		writeError(w, http.StatusServiceUnavailable, "office preview is misconfigured")
		return
	}
	att, ok := h.loadAttachmentForRequest(w, r)
	if !ok {
		return
	}
	docType, fileType, ok := officeDocType(att.Filename)
	if !ok {
		writeError(w, http.StatusBadRequest, "attachment is not an office document")
		return
	}

	attID := uuidToString(att.ID)
	token := officeFetchToken(attID, h.cfg.OnlyOfficeFetchSecret, time.Now().Add(officeFetchTokenTTL))

	base := h.cfg.OnlyOfficeFetchBaseURL
	if base == "" {
		base = h.cfg.PublicURL
	}
	fetchURL := strings.TrimRight(base, "/") + "/api/office/" + attID + "/content?token=" + url.QueryEscape(token)

	config := map[string]any{
		"document": map[string]any{
			"fileType": fileType,
			"key":      attID, // attachments are immutable → stable cache key
			"title":    att.Filename,
			"url":      fetchURL,
			"permissions": map[string]any{
				"edit":     false,
				"download": false,
				"print":    true,
			},
		},
		"documentType": docType,
		"editorConfig": map[string]any{
			"mode": "view",
			"lang": "zh-CN",
			"customization": map[string]any{
				"chat":     false,
				"comments": false,
				"help":     false,
			},
		},
	}

	signed, err := signOfficeConfig(config, h.cfg.OnlyOfficeJWTSecret)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to sign office config")
		return
	}
	config["token"] = signed

	writeJSON(w, http.StatusOK, map[string]any{
		"document_server_url": h.cfg.OnlyOfficeDocumentServerPublicURL,
		"config":              config,
	})
}
```

- [ ] **Step 4: Register the route**

In `server/cmd/server/router.go`, inside the `RequireWorkspaceMember` group, right after `r.Get("/api/attachments/{id}", h.GetAttachmentByID)` (line ~830), add:

```go
			r.Get("/api/attachments/{id}/office-config", h.GetOfficeConfig)
```

- [ ] **Step 5: Run test to verify it passes**

Run: `cd server && go test ./internal/handler/ -run TestGetOfficeConfig && go build ./...`
Expected: PASS and build succeeds.

- [ ] **Step 6: Commit**

```bash
git add server/internal/handler/office.go server/internal/handler/office_test.go server/cmd/server/router.go
git commit -m "feat(office): add office-config endpoint"
```

---

## Task B5: `GET /api/office/{id}/content` public fetch endpoint + route

**Files:**
- Modify: `server/internal/handler/office.go`
- Modify: `server/internal/handler/office_test.go`
- Modify: `server/cmd/server/router.go` (public, OUTSIDE the Auth group, near line 533)

**Interfaces:**
- Consumes: `verifyOfficeFetchToken`, `officeSecretsReady` (B4); `util.ParseUUID`; `h.Queries.GetAttachmentByIDOnly`; `h.Storage.KeyFromURL`, `h.Storage.GetReader`.
- Produces: `verifyOnlyOfficeAuthHeader(authHeader, secret string) bool`; `func (h *Handler) ServeOfficeContent(w http.ResponseWriter, r *http.Request)`.

> The Document Server in this deployment has `JWT_ENABLED=true`, so it always sends the `Authorization` header on its fetch. The whole integration already hard-depends on JWT being on (the browser config is JWT-signed too), so requiring this header introduces no new coupling.

- [ ] **Step 1: Write the failing test**

Append to `server/internal/handler/office_test.go` (add imports `bytes`, `io`):

```go
type officeContentStorage struct {
	mockStorage // reuse the stub from file_test.go (same package)
	body        []byte
}

func (s *officeContentStorage) GetReader(_ context.Context, _ string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(s.body)), nil
}

func TestServeOfficeContent(t *testing.T) {
	prev := testHandler.cfg
	prevStorage := testHandler.Storage
	testHandler.cfg.OnlyOfficeEnabled = true
	testHandler.cfg.OnlyOfficeFetchSecret = "fetch-secret"
	testHandler.cfg.OnlyOfficeJWTSecret = "jwt-secret"
	testHandler.cfg.OnlyOfficeFetchBaseURL = "https://api.example.com"
	testHandler.Storage = &officeContentStorage{body: []byte("PK\x03\x04 fake docx bytes")}
	t.Cleanup(func() { testHandler.cfg = prev; testHandler.Storage = prevStorage })

	id := seedOfficeAttachment(t, "Report.docx",
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document")
	good := officeFetchToken(id, "fetch-secret", time.Now().Add(5*time.Minute))

	// The DS adds an OnlyOffice JWT (signed with the JWT secret) to its fetch.
	dsAuth, err := signOfficeConfig(map[string]any{"url": "doc"}, "jwt-secret")
	if err != nil {
		t.Fatalf("mint ds auth: %v", err)
	}
	officeReq := func(token string) *http.Request {
		r := withURLParam(httptest.NewRequest("GET", "/api/office/"+id+"/content?token="+token, nil), "id", id)
		r.Header.Set("Authorization", "Bearer "+dsAuth)
		return r
	}

	w := httptest.NewRecorder()
	testHandler.ServeOfficeContent(w, officeReq(good))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if !bytes.Equal(w.Body.Bytes(), []byte("PK\x03\x04 fake docx bytes")) {
		t.Errorf("body mismatch: %q", w.Body.String())
	}

	// Valid token but NO Authorization header → 403 (defense-in-depth).
	wNoAuth := httptest.NewRecorder()
	testHandler.ServeOfficeContent(wNoAuth, withURLParam(httptest.NewRequest("GET", "/api/office/"+id+"/content?token="+good, nil), "id", id))
	if wNoAuth.Code != http.StatusForbidden {
		t.Errorf("missing Authorization: expected 403, got %d", wNoAuth.Code)
	}

	// Expired token → 403 (fails at the token stage, before the header check).
	expired := officeFetchToken(id, "fetch-secret", time.Now().Add(-time.Minute))
	wexp := httptest.NewRecorder()
	testHandler.ServeOfficeContent(wexp, officeReq(expired))
	if wexp.Code != http.StatusForbidden {
		t.Errorf("expired: expected 403, got %d", wexp.Code)
	}

	// Token for a different id → 403.
	wbad := httptest.NewRecorder()
	testHandler.ServeOfficeContent(wbad, officeReq("deadbeef"))
	if wbad.Code != http.StatusForbidden {
		t.Errorf("bad token: expected 403, got %d", wbad.Code)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd server && go test ./internal/handler/ -run TestServeOfficeContent`
Expected: FAIL — `undefined: ServeOfficeContent`.

- [ ] **Step 3: Write minimal implementation**

Add `"io"`, `"strconv"` (strconv already added in B2), and `"github.com/multica-ai/multica/server/internal/util"` to `office.go` imports, then append:

```go
const maxOfficePreviewSize = 50 << 20 // 50 MiB

// verifyOnlyOfficeAuthHeader reports whether the Authorization header carries a
// valid OnlyOffice HS256 JWT signed with the DS's JWT secret. The DS adds this
// header to its outgoing document fetch when JWT is enabled. We verify the
// SIGNATURE only (not specific claims) to stay robust across DS versions. This
// is defense-in-depth on top of the HMAC query token: even if the token leaks
// via ingress query logs, an attacker without the JWT secret cannot forge this.
func verifyOnlyOfficeAuthHeader(authHeader, secret string) bool {
	raw := strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
	if raw == "" {
		return false
	}
	tok, err := jwt.Parse(raw, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, jwt.ErrSignatureInvalid
		}
		return []byte(secret), nil
	})
	return err == nil && tok.Valid
}

// ServeOfficeContent streams an attachment's bytes to the OnlyOffice Document
// Server. This is a PUBLIC route, mounted OUTSIDE the user-auth middleware:
// the DS has no user session and sends its OWN Authorization JWT, which must
// never reach Multica's auth layer. Authorization here is the HMAC fetch token
// PLUS the DS's OnlyOffice Authorization JWT.
func (h *Handler) ServeOfficeContent(w http.ResponseWriter, r *http.Request) {
	if !h.cfg.OnlyOfficeEnabled {
		writeError(w, http.StatusNotFound, "office preview is not enabled")
		return
	}
	// Enabled but misconfigured → fail closed (503): an empty FetchSecret/JWT
	// secret would make the token / Authorization checks below trivially pass.
	if !h.officeSecretsReady() {
		writeError(w, http.StatusServiceUnavailable, "office preview is misconfigured")
		return
	}
	attachmentID := chi.URLParam(r, "id")
	token := r.URL.Query().Get("token")
	if token == "" || !verifyOfficeFetchToken(token, attachmentID, h.cfg.OnlyOfficeFetchSecret, time.Now()) {
		writeError(w, http.StatusForbidden, "invalid or expired token")
		return
	}
	// Defense-in-depth: the request must also carry a valid OnlyOffice JWT in
	// the Authorization header (the DS signs this with the shared JWT secret).
	if !verifyOnlyOfficeAuthHeader(r.Header.Get("Authorization"), h.cfg.OnlyOfficeJWTSecret) {
		writeError(w, http.StatusForbidden, "invalid document server signature")
		return
	}
	attUUID, err := util.ParseUUID(attachmentID)
	if err != nil {
		writeError(w, http.StatusForbidden, "invalid or expired token")
		return
	}
	att, err := h.Queries.GetAttachmentByIDOnly(r.Context(), attUUID)
	if err != nil {
		writeError(w, http.StatusNotFound, "attachment not found")
		return
	}
	if att.SizeBytes > maxOfficePreviewSize {
		writeError(w, http.StatusRequestEntityTooLarge, "file too large for preview")
		return
	}
	if h.Storage == nil {
		writeError(w, http.StatusServiceUnavailable, "storage not configured")
		return
	}
	reader, err := h.Storage.GetReader(r.Context(), h.Storage.KeyFromURL(att.Url))
	if err != nil {
		writeError(w, http.StatusNotFound, "attachment not found")
		return
	}
	defer reader.Close()
	w.Header().Set("Content-Type", att.ContentType)
	w.Header().Set("Content-Length", strconv.FormatInt(att.SizeBytes, 10))
	io.Copy(w, reader)
}
```

Add `"github.com/go-chi/chi/v5"` to `office.go` imports (used for `chi.URLParam`).

- [ ] **Step 4: Register the PUBLIC route**

In `server/cmd/server/router.go`, register this route **outside** the Auth group. Immediately **before** the `r.Group(func(r chi.Router) {` block that calls `r.Use(middleware.Auth(queries, patCache, cloudPATVerifier))` (line ~533), add:

```go
	// OnlyOffice file fetch — PUBLIC, no user auth. The Document Server
	// authenticates with the short-lived HMAC token in the query string and
	// sends its own Authorization JWT, which must not hit middleware.Auth.
	r.Get("/api/office/{id}/content", h.ServeOfficeContent)
```

- [ ] **Step 5: Run test to verify it passes**

Run: `cd server && go test ./internal/handler/ -run TestServeOfficeContent && go build ./...`
Expected: PASS and build succeeds.

- [ ] **Step 6: Run the whole handler office suite + vet**

Run: `cd server && go test ./internal/handler/ -run 'Office|OfficeContent' && go vet ./internal/handler/ ./cmd/server/`
Expected: PASS, no vet complaints.

- [ ] **Step 7: Commit**

```bash
git add server/internal/handler/office.go server/internal/handler/office_test.go server/cmd/server/router.go
git commit -m "feat(office): add public token-authorized file fetch endpoint"
```

---

## Task F1: Frontend office preview-kind detection

**Files:**
- Modify: `packages/views/editor/utils/preview.ts`
- Create: `packages/views/editor/utils/preview.test.ts`

**Interfaces:**
- Produces: `PreviewKind` now includes `"office"`; `getPreviewKind(contentType, filename)` returns `"office"` for office files (CSV included).

- [ ] **Step 1: Write the failing test**

Create `packages/views/editor/utils/preview.test.ts`:

```ts
import { describe, it, expect } from "vitest";
import { getPreviewKind } from "./preview";

describe("getPreviewKind — office", () => {
  it("docx → office (by content-type)", () => {
    expect(
      getPreviewKind(
        "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
        "Report.docx",
      ),
    ).toBe("office");
  });
  it("xlsx → office (by extension, empty content-type)", () => {
    expect(getPreviewKind("", "data.xlsx")).toBe("office");
  });
  it("pptx → office", () => {
    expect(getPreviewKind("", "deck.pptx")).toBe("office");
  });
  it("csv → office, even when sniffed as text/plain", () => {
    expect(getPreviewKind("text/plain", "data.csv")).toBe("office");
  });
  it("tsv stays text (not office)", () => {
    expect(getPreviewKind("", "data.tsv")).toBe("text");
  });
  it("png is unaffected", () => {
    expect(getPreviewKind("image/png", "a.png")).toBe("image");
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `pnpm --filter @multica/views exec vitest run editor/utils/preview.test.ts`
Expected: FAIL — `csv → office` returns `"text"`; `xlsx → office` returns `null`.

- [ ] **Step 3: Add the office kind + detection**

In `packages/views/editor/utils/preview.ts`:

(a) Extend the `PreviewKind` union (after `"text"`):

```ts
export type PreviewKind =
  | "image"
  | "pdf"
  | "video"
  | "audio"
  | "markdown"
  | "html"
  | "text"
  | "office";
```

(b) Remove `"csv"` from `TEXT_EXTENSIONS` (line ~92). The set's first line becomes:

```ts
  "md", "markdown", "txt", "log", "tsv",
```

(c) After the `IMAGE_EXTS` declaration block, add the office sets:

```ts
// Office documents handled by the embedded OnlyOffice viewer. CSV is included
// here (design decision) — it must resolve to "office", not "text".
const OFFICE_EXTS = new Set<string>([
  "doc", "docx", "odt", "rtf",
  "xls", "xlsx", "ods", "csv",
  "ppt", "pptx", "odp",
]);
const OFFICE_CONTENT_TYPES = new Set<string>([
  "application/msword",
  "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
  "application/vnd.oasis.opendocument.text",
  "application/rtf",
  "application/vnd.ms-excel",
  "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
  "application/vnd.oasis.opendocument.spreadsheet",
  "text/csv",
  "application/vnd.ms-powerpoint",
  "application/vnd.openxmlformats-officedocument.presentationml.presentation",
  "application/vnd.oasis.opendocument.presentation",
]);
```

(d) In `getPreviewKind`, add the office branch immediately AFTER the image branch and BEFORE the markdown branch:

```ts
  if (ct.startsWith("image/") || (ext && IMAGE_EXTS.has(ext))) return "image";

  // Office documents (Word/Excel/PPT + ODF + CSV) → OnlyOffice viewer.
  // Must come before the text branch so .csv (often sniffed text/plain)
  // resolves to "office" rather than "text".
  if (OFFICE_CONTENT_TYPES.has(ct) || (ext && OFFICE_EXTS.has(ext))) {
    return "office";
  }
```

> Note: leave the backend `isTextPreviewable` whitelist (`server/internal/handler/file.go`) untouched. CSV stays accepted there so older desktop clients (which still treat csv as text) keep working — an API-compatibility safety margin. New clients route csv to office before ever calling the text proxy.

- [ ] **Step 4: Run test to verify it passes**

Run: `pnpm --filter @multica/views exec vitest run editor/utils/preview.test.ts`
Expected: PASS (all 6).

- [ ] **Step 5: Commit**

```bash
git add packages/views/editor/utils/preview.ts packages/views/editor/utils/preview.test.ts
git commit -m "feat(office): detect office attachments as a new preview kind"
```

---

## Task F2: OfficeConfig type, schema, and API client method

**Files:**
- Create: `packages/core/types/office.ts`
- Modify: `packages/core/types/index.ts`
- Modify: `packages/core/api/schemas.ts`
- Create: `packages/core/api/office-schema.test.ts`
- Modify: `packages/core/api/client.ts`

**Interfaces:**
- Produces: `OfficeConfig`; `OfficeConfigResponseSchema`, `EMPTY_OFFICE_CONFIG`; `ApiClient.getOfficeConfig(id: string): Promise<OfficeConfig>`.

- [ ] **Step 1: Write the failing test**

Create `packages/core/api/office-schema.test.ts`:

```ts
import { describe, it, expect } from "vitest";
import { OfficeConfigResponseSchema, EMPTY_OFFICE_CONFIG } from "./schemas";
import { parseWithFallback } from "./schema";

const opts = { endpoint: "GET /api/attachments/{id}/office-config" };

describe("OfficeConfigResponseSchema", () => {
  it("parses a valid response", () => {
    const r = parseWithFallback(
      { document_server_url: "https://weboffice.x", config: { documentType: "word", token: "jwt" } },
      OfficeConfigResponseSchema,
      EMPTY_OFFICE_CONFIG,
      opts,
    );
    expect(r.document_server_url).toBe("https://weboffice.x");
    expect(r.config.documentType).toBe("word");
  });

  it("falls back when document_server_url is missing", () => {
    const r = parseWithFallback({ config: {} }, OfficeConfigResponseSchema, EMPTY_OFFICE_CONFIG, opts);
    expect(r).toBe(EMPTY_OFFICE_CONFIG);
  });

  it("falls back on a null body", () => {
    const r = parseWithFallback(null, OfficeConfigResponseSchema, EMPTY_OFFICE_CONFIG, opts);
    expect(r).toBe(EMPTY_OFFICE_CONFIG);
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `pnpm --filter @multica/core exec vitest run api/office-schema.test.ts`
Expected: FAIL — `OfficeConfigResponseSchema` is not exported.

- [ ] **Step 3: Add the type**

Create `packages/core/types/office.ts`:

```ts
export interface OfficeConfig {
  /** Public Document Server base URL — the frontend loads api.js from here. */
  document_server_url: string;
  /**
   * The OnlyOffice DocEditor config, already JWT-signed by the backend
   * (`config.token`). Opaque to the client — passed straight to DocsAPI.
   */
  config: Record<string, unknown>;
}
```

Add to `packages/core/types/index.ts` (alongside the other `export * from "./..."` lines):

```ts
export * from "./office";
```

- [ ] **Step 4: Add the schema**

In `packages/core/api/schemas.ts`, add `OfficeConfig` to the type import from `../types`, then after `EMPTY_ATTACHMENT` add:

```ts
export const OfficeConfigResponseSchema = z
  .object({
    document_server_url: z.string(),
    config: z.record(z.string(), z.unknown()),
  })
  .loose();

export const EMPTY_OFFICE_CONFIG: OfficeConfig = {
  document_server_url: "",
  config: {},
};
```

- [ ] **Step 5: Run test to verify it passes**

Run: `pnpm --filter @multica/core exec vitest run api/office-schema.test.ts`
Expected: PASS (all 3).

- [ ] **Step 6: Add the client method**

In `packages/core/api/client.ts`, add `OfficeConfigResponseSchema, EMPTY_OFFICE_CONFIG` to the existing import from `./schemas`, add `OfficeConfig` to the type import from `../types`, and add the method next to `getAttachment` (around line 1835):

```ts
  async getOfficeConfig(id: string): Promise<OfficeConfig> {
    const raw = await this.fetch<unknown>(`/api/attachments/${id}/office-config`);
    return parseWithFallback(raw, OfficeConfigResponseSchema, EMPTY_OFFICE_CONFIG, {
      endpoint: "GET /api/attachments/{id}/office-config",
    });
  }
```

- [ ] **Step 7: Verify typecheck**

Run: `pnpm --filter @multica/core typecheck && pnpm --filter @multica/core exec vitest run api/office-schema.test.ts`
Expected: typecheck clean; tests PASS.

- [ ] **Step 8: Commit**

```bash
git add packages/core/types/office.ts packages/core/types/index.ts packages/core/api/schemas.ts packages/core/api/office-schema.test.ts packages/core/api/client.ts
git commit -m "feat(office): add OfficeConfig type, schema, and client method"
```

---

## Task F3: DocsAPI script loader

**Files:**
- Create: `packages/views/editor/utils/docs-api-loader.ts`
- Create: `packages/views/editor/utils/docs-api-loader.test.ts`

**Interfaces:**
- Produces: `loadDocsApi(documentServerUrl: string): Promise<DocsApi>`; types `DocsApi`, `DocsApiEditor`; test seam `__resetDocsApiLoaderForTest()`.

- [ ] **Step 1: Write the failing test**

Create `packages/views/editor/utils/docs-api-loader.test.ts`:

```ts
import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { loadDocsApi, __resetDocsApiLoaderForTest } from "./docs-api-loader";

describe("loadDocsApi", () => {
  beforeEach(() => {
    __resetDocsApiLoaderForTest();
    delete (window as unknown as { DocsAPI?: unknown }).DocsAPI;
  });
  afterEach(() => {
    document.head.querySelectorAll("script").forEach((s) => s.remove());
  });

  it("injects one script for concurrent calls and resolves with DocsAPI", async () => {
    const p1 = loadDocsApi("https://weboffice.x");
    const p2 = loadDocsApi("https://weboffice.x");
    const scripts = document.head.querySelectorAll('script[src*="api.js"]');
    expect(scripts.length).toBe(1);

    const fakeApi = { DocEditor: vi.fn() };
    (window as unknown as { DocsAPI: unknown }).DocsAPI = fakeApi;
    scripts[0].dispatchEvent(new Event("load"));

    await expect(p1).resolves.toBe(fakeApi);
    await expect(p2).resolves.toBe(fakeApi);
  });

  it("rejects on script error", async () => {
    const p = loadDocsApi("https://weboffice.y");
    const script = document.head.querySelector('script[src*="api.js"]')!;
    script.dispatchEvent(new Event("error"));
    await expect(p).rejects.toThrow(/failed to load/);
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `pnpm --filter @multica/views exec vitest run editor/utils/docs-api-loader.test.ts`
Expected: FAIL — module not found.

- [ ] **Step 3: Write minimal implementation**

Create `packages/views/editor/utils/docs-api-loader.ts`:

```ts
// Minimal typing for the OnlyOffice browser API. We use only the DocEditor
// constructor and its destroyEditor lifecycle method.
export interface DocsApiEditor {
  destroyEditor(): void;
}
export interface DocsApi {
  DocEditor: new (
    placeholderId: string,
    config: Record<string, unknown>,
  ) => DocsApiEditor;
}

declare global {
  interface Window {
    DocsAPI?: DocsApi;
  }
}

// One in-flight promise per documentServerUrl so concurrent previews share a
// single <script> injection. Resolves when window.DocsAPI is available.
const loaders = new Map<string, Promise<DocsApi>>();

export function loadDocsApi(documentServerUrl: string): Promise<DocsApi> {
  if (typeof window !== "undefined" && window.DocsAPI) {
    return Promise.resolve(window.DocsAPI);
  }
  const cached = loaders.get(documentServerUrl);
  if (cached) return cached;

  const src = `${documentServerUrl.replace(/\/$/, "")}/web-apps/apps/api/documents/api.js`;
  const promise = new Promise<DocsApi>((resolve, reject) => {
    const script = document.createElement("script");
    script.src = src;
    script.async = true;
    script.onload = () => {
      if (window.DocsAPI) resolve(window.DocsAPI);
      else reject(new Error("DocsAPI not available after api.js load"));
    };
    script.onerror = () => {
      loaders.delete(documentServerUrl);
      reject(new Error(`failed to load OnlyOffice api.js from ${src}`));
    };
    document.head.appendChild(script);
  });
  loaders.set(documentServerUrl, promise);
  return promise;
}

// Test seam: reset the module-level cache between tests.
export function __resetDocsApiLoaderForTest(): void {
  loaders.clear();
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `pnpm --filter @multica/views exec vitest run editor/utils/docs-api-loader.test.ts`
Expected: PASS (both).

- [ ] **Step 5: Commit**

```bash
git add packages/views/editor/utils/docs-api-loader.ts packages/views/editor/utils/docs-api-loader.test.ts
git commit -m "feat(office): add one-shot OnlyOffice api.js loader"
```

---

## Task F4: OfficeAttachmentPreview component (+ extract fallback)

**Files:**
- Create: `packages/views/editor/attachment-preview-fallback.tsx`
- Modify: `packages/views/editor/attachment-preview-modal.tsx` (use the extracted fallback)
- Create: `packages/views/editor/office-attachment-preview.tsx`
- Create: `packages/views/editor/office-attachment-preview.test.tsx`

**Interfaces:**
- Consumes: `loadDocsApi`, `DocsApiEditor` (F3); `api.getOfficeConfig` (F2).
- Produces: `UnsupportedFallback({ message, onDownload })` (moved, exported); `OfficeAttachmentPreview({ attachmentId, filename, onDownload })`.

- [ ] **Step 1: Extract the shared fallback (refactor, no behavior change)**

Create `packages/views/editor/attachment-preview-fallback.tsx` with the function currently at `attachment-preview-modal.tsx:631-653`, verbatim, plus its imports:

```tsx
"use client";

import { Download, FileText } from "lucide-react";
import { useT } from "../i18n";

export function UnsupportedFallback({
  message,
  onDownload,
}: {
  message: string;
  onDownload: () => void;
}) {
  const { t } = useT("editor");
  return (
    <div className="flex h-full flex-col items-center justify-center gap-3 px-8 text-center">
      <FileText className="size-8 text-muted-foreground" />
      <p className="text-sm text-muted-foreground">{message}</p>
      <button
        type="button"
        className="inline-flex items-center gap-2 rounded-md border border-border bg-background px-3 py-1.5 text-sm transition-colors hover:bg-muted"
        onClick={onDownload}
      >
        <Download className="size-4" />
        {t(($) => $.image.download)}
      </button>
    </div>
  );
}
```

> Note: this file is a sibling of the modal (both in `packages/views/editor/`), so it uses the **same** i18n import as the modal: `import { useT } from "../i18n";` (resolves to `packages/views/i18n`).

In `attachment-preview-modal.tsx`: delete the local `function UnsupportedFallback(...) { ... }` (lines 631-653) and add an import near the other local imports (after line 74):

```tsx
import { UnsupportedFallback } from "./attachment-preview-fallback";
```

Leave the `FileText` / `Download` imports in the modal only if they are still used elsewhere in that file; if the editor flags them as unused, remove them from the modal's lucide import.

- [ ] **Step 2: Verify the refactor is green**

Run: `pnpm --filter @multica/views typecheck`
Expected: clean (no unused-import or missing-symbol errors).

- [ ] **Step 3: Write the failing component test**

Create `packages/views/editor/office-attachment-preview.test.tsx`:

```tsx
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, waitFor, cleanup } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

// vi.mock factories are hoisted ABOVE these imports — referencing a plain
// top-level `const x = vi.fn()` from inside a factory throws at load time. Use
// vi.hoisted() so the mock fns exist when the factory runs (CLAUDE.md mocking
// convention).
const mocks = vi.hoisted(() => {
  const destroyEditor = vi.fn();
  return {
    getOfficeConfig: vi.fn(),
    destroyEditor,
    DocEditor: vi.fn(() => ({ destroyEditor })),
    loadDocsApi: vi.fn(),
  };
});
vi.mock("@multica/core/api", () => ({
  api: { getOfficeConfig: mocks.getOfficeConfig },
}));
vi.mock("./utils/docs-api-loader", () => ({
  loadDocsApi: mocks.loadDocsApi,
}));

import { OfficeAttachmentPreview } from "./office-attachment-preview";

function renderWithClient(ui: React.ReactElement) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={client}>{ui}</QueryClientProvider>);
}

describe("OfficeAttachmentPreview", () => {
  beforeEach(() => {
    mocks.getOfficeConfig.mockReset();
    mocks.DocEditor.mockClear();
    mocks.destroyEditor.mockReset();
    mocks.loadDocsApi.mockReset();
  });
  afterEach(() => cleanup());

  it("loads config, instantiates DocEditor, and destroys on unmount", async () => {
    mocks.getOfficeConfig.mockResolvedValue({
      document_server_url: "https://weboffice.x",
      config: { documentType: "word", token: "jwt" },
    });
    mocks.loadDocsApi.mockResolvedValue({ DocEditor: mocks.DocEditor });

    const { unmount } = renderWithClient(
      <OfficeAttachmentPreview attachmentId="att-1" filename="r.docx" onDownload={() => {}} />,
    );

    await waitFor(() => expect(mocks.getOfficeConfig).toHaveBeenCalledWith("att-1"));
    await waitFor(() => expect(mocks.loadDocsApi).toHaveBeenCalledWith("https://weboffice.x"));
    await waitFor(() => expect(mocks.DocEditor).toHaveBeenCalledTimes(1));
    // Second constructor arg is the signed config.
    expect(mocks.DocEditor.mock.calls[0][1]).toMatchObject({ documentType: "word", token: "jwt" });

    unmount();
    expect(mocks.destroyEditor).toHaveBeenCalledTimes(1);
  });

  it("renders the download fallback when config has no server url", async () => {
    mocks.getOfficeConfig.mockResolvedValue({ document_server_url: "", config: {} });
    const onDownload = vi.fn();
    const { findByRole } = renderWithClient(
      <OfficeAttachmentPreview attachmentId="att-2" filename="r.docx" onDownload={onDownload} />,
    );
    (await findByRole("button")).click();
    expect(onDownload).toHaveBeenCalled();
    expect(mocks.loadDocsApi).not.toHaveBeenCalled();
  });

  it("renders the download fallback when api.js fails to load", async () => {
    mocks.getOfficeConfig.mockResolvedValue({ document_server_url: "https://weboffice.x", config: {} });
    mocks.loadDocsApi.mockRejectedValue(new Error("failed to load"));
    const onDownload = vi.fn();
    const { findByRole } = renderWithClient(
      <OfficeAttachmentPreview attachmentId="att-3" filename="r.docx" onDownload={onDownload} />,
    );
    (await findByRole("button")).click();
    expect(onDownload).toHaveBeenCalled();
  });
});
```

- [ ] **Step 4: Run test to verify it fails**

Run: `pnpm --filter @multica/views exec vitest run editor/office-attachment-preview.test.tsx`
Expected: FAIL — `OfficeAttachmentPreview` module not found.

- [ ] **Step 5: Write the component**

Create `packages/views/editor/office-attachment-preview.tsx`:

```tsx
"use client";

import { useEffect, useId, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "@multica/core/api";
import { Loader2 } from "lucide-react";
import { useT } from "../i18n";
import { UnsupportedFallback } from "./attachment-preview-fallback";
import { loadDocsApi, type DocsApiEditor } from "./utils/docs-api-loader";

export function OfficeAttachmentPreview({
  attachmentId,
  filename,
  onDownload,
}: {
  attachmentId: string;
  filename: string;
  onDownload: () => void;
}) {
  const { t } = useT("editor");
  // DocEditor needs a DOM-id-safe placeholder; useId() yields ":r0:" — strip
  // the colons so the id is also a valid CSS selector.
  const placeholderId = `office-${useId().replace(/:/g, "_")}`;
  const editorRef = useRef<DocsApiEditor | null>(null);
  const [loadError, setLoadError] = useState(false);

  const { data, isPending, isError } = useQuery({
    queryKey: ["office-config", attachmentId],
    queryFn: () => api.getOfficeConfig(attachmentId),
    enabled: !!attachmentId,
    retry: false,
    staleTime: 60_000,
  });

  useEffect(() => {
    if (!data?.document_server_url) return;
    let cancelled = false;
    setLoadError(false);
    loadDocsApi(data.document_server_url)
      .then((docs) => {
        if (cancelled) return;
        editorRef.current = new docs.DocEditor(placeholderId, data.config);
      })
      .catch(() => {
        // api.js failed to load (DS unreachable / CSP / network). Surface the
        // download fallback instead of leaving a blank placeholder.
        if (!cancelled) setLoadError(true);
      });
    return () => {
      cancelled = true;
      try {
        editorRef.current?.destroyEditor();
      } catch {
        // Editor may not have finished initializing; destroy is best-effort.
      }
      editorRef.current = null;
    };
  }, [data, placeholderId]);

  if (isPending) {
    return (
      <div className="flex h-full w-full items-center justify-center">
        <Loader2 className="size-6 animate-spin text-muted-foreground" />
      </div>
    );
  }
  if (isError || !data?.document_server_url || loadError) {
    return (
      <UnsupportedFallback
        message={t(($) => $.attachment.preview_unsupported)}
        onDownload={onDownload}
      />
    );
  }
  return <div id={placeholderId} className="h-full w-full" />;
}
```

- [ ] **Step 6: Run test to verify it passes**

Run: `pnpm --filter @multica/views exec vitest run editor/office-attachment-preview.test.tsx`
Expected: PASS (all three: instantiate/destroy, no-server-url fallback, api.js-load-failure fallback).

- [ ] **Step 7: Commit**

```bash
git add packages/views/editor/attachment-preview-fallback.tsx packages/views/editor/attachment-preview-modal.tsx packages/views/editor/office-attachment-preview.tsx packages/views/editor/office-attachment-preview.test.tsx
git commit -m "feat(office): add OnlyOffice viewer component"
```

---

## Task F5: Wire the office kind into the preview modal

**Files:**
- Modify: `packages/views/editor/attachment-preview-modal.tsx`
- Create: `packages/views/editor/office-attachment-preview.modal.test.tsx`

**Interfaces:**
- Consumes: `OfficeAttachmentPreview` (F4); `getPreviewKind` returning `"office"` (F1); `useAttachmentPreview` (existing).

- [ ] **Step 1: Write the failing test**

Create `packages/views/editor/office-attachment-preview.modal.test.tsx`:

```tsx
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, waitFor, cleanup } from "@testing-library/react";
import { useEffect } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { Attachment } from "@multica/core/types";

// Mock fn referenced from the factory must come from vi.hoisted() (the factory
// is hoisted above this line). The loadDocsApi factory uses an inline vi.fn()
// so it needs no hoisting.
const mocks = vi.hoisted(() => ({ getOfficeConfig: vi.fn() }));
vi.mock("@multica/core/api", async (importOriginal) => {
  const actual = await importOriginal<Record<string, unknown>>();
  return {
    ...actual,
    api: { getOfficeConfig: mocks.getOfficeConfig },
  };
});
vi.mock("./utils/docs-api-loader", () => ({
  loadDocsApi: vi.fn(() => new Promise(() => {})), // never resolves; we only assert config fetch
}));

import { useAttachmentPreview } from "./attachment-preview-modal";

const officeAttachment: Attachment = {
  id: "att-office-1",
  workspace_id: "ws",
  issue_id: null,
  comment_id: null,
  chat_session_id: null,
  chat_message_id: null,
  uploader_type: "member",
  uploader_id: "u",
  filename: "Report.docx",
  url: "https://cdn.x/Report.docx",
  download_url: "https://cdn.x/Report.docx",
  markdown_url: "",
  content_type:
    "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
  size_bytes: 100,
  created_at: "",
};

function Harness({ attachment }: { attachment: Attachment }) {
  const preview = useAttachmentPreview();
  useEffect(() => {
    preview.open({ kind: "full", attachment });
  }, []); // eslint-disable-line react-hooks/exhaustive-deps
  return <>{preview.modal}</>;
}

function renderWithClient(ui: React.ReactElement) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={client}>{ui}</QueryClientProvider>);
}

describe("AttachmentPreviewModal — office kind", () => {
  beforeEach(() => mocks.getOfficeConfig.mockReset());
  afterEach(() => cleanup());

  it("mounts the OnlyOffice viewer for an office attachment", async () => {
    mocks.getOfficeConfig.mockResolvedValue({ document_server_url: "https://weboffice.x", config: {} });
    renderWithClient(<Harness attachment={officeAttachment} />);
    await waitFor(() => expect(mocks.getOfficeConfig).toHaveBeenCalledWith("att-office-1"));
  });
});
```

> If `useAttachmentPreview` is not exported from `attachment-preview-modal.tsx`, add `export` to its declaration as part of Step 3 (it is the module's public hook).

- [ ] **Step 2: Run test to verify it fails**

Run: `pnpm --filter @multica/views exec vitest run editor/office-attachment-preview.modal.test.tsx`
Expected: FAIL — `getOfficeConfig` never called (office kind not dispatched yet).

- [ ] **Step 3: Add the office dispatch**

In `packages/views/editor/attachment-preview-modal.tsx`:

(a) Import the component (after the F4 fallback import):

```tsx
import { OfficeAttachmentPreview } from "./office-attachment-preview";
```

(b) In `PreviewContent`, extend the "needs an attachment id" guard to include `office` (the existing guard checks markdown/html/text — around line 482):

```tsx
  if (
    (kind === "markdown" || kind === "html" || kind === "text" || kind === "office") &&
    !state.attachmentId
  ) {
    return (
      <UnsupportedFallback
        message={t(($) => $.attachment.preview_unsupported)}
        onDownload={onDownload}
      />
    );
  }
```

(c) Add a `case "office":` to the `switch (kind)` (e.g. after the `case "text":` block):

```tsx
    case "office":
      return (
        <OfficeAttachmentPreview
          attachmentId={state.attachmentId!}
          filename={state.filename}
          onDownload={onDownload}
        />
      );
```

(d) Do NOT add `"office"` to `URL_ONLY_KINDS` — office needs the attachment id to fetch its config, so url-only sources correctly cannot open it.

(e) (Optional doc) Update the file's top comment that says "Handles 7 PreviewKinds" to 8 and add an `office` bullet.

- [ ] **Step 4: Run test to verify it passes**

Run: `pnpm --filter @multica/views exec vitest run editor/office-attachment-preview.modal.test.tsx`
Expected: PASS.

- [ ] **Step 5: Run the full views suite (sibling tests assert on these files)**

Run: `pnpm --filter @multica/views test`
Expected: PASS — no regressions in the editor/attachment suites.

- [ ] **Step 6: Commit**

```bash
git add packages/views/editor/attachment-preview-modal.tsx packages/views/editor/office-attachment-preview.modal.test.tsx
git commit -m "feat(office): render OnlyOffice viewer in the attachment preview modal"
```

---

## Task I1: Helm config wiring

**Files:**
- Modify: `deploy/helm/multica/values.yaml` (`backend.config` block)
- Modify: `deploy/helm/multica/templates/configmap.yaml` (`data` block)

**Interfaces:**
- Consumes: the env var names from Task B1 (`ONLYOFFICE_ENABLED`, `ONLYOFFICE_DOCUMENT_SERVER_PUBLIC_URL`, `ONLYOFFICE_FETCH_BASE_URL`).

- [ ] **Step 1: Add values keys**

In `deploy/helm/multica/values.yaml`, inside `backend.config:` (after `localUploadBaseUrl: ...`), add:

```yaml
    # OnlyOffice document preview (Word/Excel/PPT/CSV). Empty/false disables it.
    # The JWT/fetch secrets are NOT here — put ONLYOFFICE_JWT_SECRET and
    # ONLYOFFICE_FETCH_SECRET in the Secret named by `.Values.existingSecret`.
    onlyofficeEnabled: false
    onlyofficeDocumentServerPublicUrl: ""
    # MUST be a public, non-private-IP host — the Document Server blocks
    # private/reserved addresses when fetching the file (e.g. https://multica.example.com).
    onlyofficeFetchBaseUrl: ""
```

- [ ] **Step 2: Add ConfigMap entries**

In `deploy/helm/multica/templates/configmap.yaml`, inside `data:` (after the `LOCAL_UPLOAD_BASE_URL` line), add:

```yaml
  ONLYOFFICE_ENABLED: {{ .Values.backend.config.onlyofficeEnabled | quote }}
  ONLYOFFICE_DOCUMENT_SERVER_PUBLIC_URL: {{ .Values.backend.config.onlyofficeDocumentServerPublicUrl | quote }}
  ONLYOFFICE_FETCH_BASE_URL: {{ .Values.backend.config.onlyofficeFetchBaseUrl | quote }}
```

> The backend Deployment already does `envFrom: - configMapRef` and `- secretRef` (backend.yaml:66-70), so these ConfigMap keys and the two Secret keys flow into the container with no `backend.yaml` change. The two secrets must be added to the existing Secret object out-of-band (operator-managed): `ONLYOFFICE_JWT_SECRET` (= the Document Server's `JWT_SECRET`) and `ONLYOFFICE_FETCH_SECRET` (new, independent).

- [ ] **Step 3: Verify the chart renders**

Run:
```bash
helm template multica deploy/helm/multica \
  --set backend.config.onlyofficeEnabled=true \
  --set backend.config.onlyofficeDocumentServerPublicUrl=https://weboffice.example.com \
  --set backend.config.onlyofficeFetchBaseUrl=https://multica.example.com \
  | grep ONLYOFFICE
```
Expected output (three lines):
```
  ONLYOFFICE_ENABLED: "true"
  ONLYOFFICE_DOCUMENT_SERVER_PUBLIC_URL: "https://weboffice.example.com"
  ONLYOFFICE_FETCH_BASE_URL: "https://multica.example.com"
```

Also run: `helm lint deploy/helm/multica`
Expected: `1 chart(s) linted, 0 chart(s) failed`.

- [ ] **Step 4: Commit**

```bash
git add deploy/helm/multica/values.yaml deploy/helm/multica/templates/configmap.yaml
git commit -m "chore(office): wire OnlyOffice config into the Helm chart"
```

---

## Final verification

- [ ] **Backend:** `cd server && go test ./internal/handler/ -run Office && go build ./... && go vet ./internal/handler/ ./cmd/server/`
- [ ] **Frontend types/tests:** `pnpm --filter @multica/core typecheck && pnpm --filter @multica/views typecheck && pnpm --filter @multica/core test && pnpm --filter @multica/views test`
- [ ] **Helm:** `helm lint deploy/helm/multica`
- [ ] **Manual smoke (staging):** upload a Chinese-language `.docx`, `.xlsx`, `.pptx`, and `.csv`; click Eye; confirm each renders in the OnlyOffice viewer with correct CJK glyphs (the pre-launch font check from the spec). If glyphs are wrong, install/register CJK fonts in the Document Server image and regenerate its font cache.

---

## Spec coverage self-check

- Embed OnlyOffice viewer, mode=view → F4 (`mode: "view"` in B4 config), F5 (modal dispatch). ✓
- Backend office-config endpoint, JWT-signed, authed/workspace-scoped → B3, B4. ✓
- Public HMAC-token fetch endpoint, outside user-auth, DS Authorization JWT validated (defense-in-depth) → B2, B5. ✓
- Fail closed when enabled-but-misconfigured (empty JWT/fetch secret or missing URL) → B4/B5 `officeSecretsReady` returns 503, never signs with "". ✓
- `document.url` uses public fetch base, not internal IP → B4 (`OnlyOfficeFetchBaseURL`), I1 (public value). ✓
- CSV → office (cell), office priority over text → B1 (`officeDocType` csv→cell), F1 (detection + drop csv from text). ✓
- Dedicated fetch secret → B1 field, B2 helpers, I1 note. ✓
- `permissions.print = true` → B4 config. ✓
- API Response Compatibility (zod + parseWithFallback + malformed test) → F2. ✓
- Graceful degradation (no Eye / download fallback) → F4 (config-error, missing-URL, AND `loadError` api.js-failure paths), F5 (id-guard), preview-kind gating in existing `attachment-card.tsx` (office requires `attachmentId`, already handled by the existing `canPreview` logic). ✓
- Max preview size (50 MB) → B5. ✓
- Helm ConfigMap + Secret wiring → I1. ✓
- CJK font validation → Final verification manual smoke. ✓
- CSP: verified live, no change needed → no task (documented in spec). ✓
