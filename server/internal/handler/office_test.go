package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestVerifyOnlyOfficeAuthHeader(t *testing.T) {
	const secret = "the-secret"

	// Helper: sign a JWT with given claims and key.
	sign := func(claims jwt.Claims, key string) string {
		tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
		s, err := tok.SignedString([]byte(key))
		if err != nil {
			t.Fatalf("sign: %v", err)
		}
		return s
	}

	// A valid JWT with an already-expired exp claim — must STILL pass (signature-only).
	expiredJWT := sign(jwt.MapClaims{"exp": time.Now().Add(-time.Hour).Unix()}, secret)
	if !verifyOnlyOfficeAuthHeader("Bearer "+expiredJWT, secret) {
		t.Error("expired-exp JWT with correct secret must pass (signature-only check)")
	}

	// A valid Bearer JWT signed with the correct secret — must pass.
	validJWT := sign(jwt.MapClaims{"sub": "docserver"}, secret)
	if !verifyOnlyOfficeAuthHeader("Bearer "+validJWT, secret) {
		t.Error("valid JWT with correct secret must pass")
	}

	// JWT signed with a DIFFERENT secret — must fail.
	wrongSecretJWT := sign(jwt.MapClaims{"sub": "docserver"}, "wrong-secret")
	if verifyOnlyOfficeAuthHeader("Bearer "+wrongSecretJWT, secret) {
		t.Error("JWT signed with wrong secret must fail")
	}

	// Empty header — must fail.
	if verifyOnlyOfficeAuthHeader("", secret) {
		t.Error("empty header must fail")
	}

	// Non-Bearer garbage string — must fail.
	if verifyOnlyOfficeAuthHeader("garbage-not-a-jwt", secret) {
		t.Error("garbage non-Bearer string must fail")
	}
}

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

func TestOfficeLang(t *testing.T) {
	cases := map[string]string{
		"zh-Hans": "zh",
		"ko":      "ko",
		"ja":      "ja",
		"en":      "en",
		"":        "en", // user has not chosen a language
		"zh-CN":   "en", // unexpected value → default
		"fr":      "en", // unsupported → default
	}
	for in, want := range cases {
		if got := officeLang(in); got != want {
			t.Errorf("officeLang(%q) = %q, want %q", in, got, want)
		}
	}
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
	if cc := w.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store (response carries a signed token)", cc)
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
	doc, ok := resp.Config["document"].(map[string]any)
	if !ok {
		t.Fatalf("config.document is not an object: %v", resp.Config["document"])
	}
	url, _ := doc["url"].(string)
	if !strings.HasPrefix(url, "https://api.example.com/api/office/"+id+"/content?token=") {
		t.Errorf("document.url = %q", url)
	}
	if tok, _ := resp.Config["token"].(string); tok == "" {
		t.Error("config.token must be a non-empty signed JWT")
	}
	// Default user (no saved language) → editor lang falls back to "en".
	ec, ok := resp.Config["editorConfig"].(map[string]any)
	if !ok {
		t.Fatalf("config.editorConfig is not an object: %v", resp.Config["editorConfig"])
	}
	if ec["lang"] != "en" {
		t.Errorf("editorConfig.lang = %v, want en (default user has no language)", ec["lang"])
	}

	// The embedded viewer shows no user chip, so no user object is supplied — the
	// signed config carries no (user-controlled) identity or avatar URL.
	if _, present := ec["user"]; present {
		t.Errorf("editorConfig.user must not be set in the embedded preview: %v", ec["user"])
	}
	cust, ok := ec["customization"].(map[string]any)
	if !ok {
		t.Fatalf("editorConfig.customization is not an object: %v", ec["customization"])
	}
	anon, _ := cust["anonymous"].(map[string]any)
	if anon["request"] != false {
		t.Errorf("customization.anonymous.request = %v, want false", anon["request"])
	}
	// The read-only preview uses the embedded viewer (no File/View/Plugins menu
	// bar) instead of the paid white-label customization.layout API, which the
	// open-source Document Server ignores. The layout key must NOT be present.
	if resp.Config["type"] != "embedded" {
		t.Errorf("config.type = %v, want embedded (Community-safe read-only viewer)", resp.Config["type"])
	}
	if _, present := cust["layout"]; present {
		t.Errorf("customization.layout must not be set (it is a paid white-label feature): %v", cust["layout"])
	}

	// Embedded-viewer controls must live under editorConfig.embedded; a top-level
	// `embedded` field is ignored by the Document Server.
	if _, present := resp.Config["embedded"]; present {
		t.Errorf("config.embedded must NOT be a top-level field (belongs under editorConfig): %v", resp.Config["embedded"])
	}
	emb, ok := ec["embedded"].(map[string]any)
	if !ok {
		t.Fatalf("editorConfig.embedded is not an object: %v", ec["embedded"])
	}
	if emb["toolbarDocked"] != "top" {
		t.Errorf("editorConfig.embedded.toolbarDocked = %v, want top", emb["toolbarDocked"])
	}
	if emb["autostart"] != "document" {
		t.Errorf("editorConfig.embedded.autostart = %v, want document (keep presentations out of the slideshow player)", emb["autostart"])
	}

	// The editor lang follows the user's saved Multica language preference.
	if _, err := testPool.Exec(context.Background(),
		`UPDATE "user" SET language = $1 WHERE id = $2`, "zh-Hans", testUserID); err != nil {
		t.Fatalf("set user language: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `UPDATE "user" SET language = NULL WHERE id = $1`, testUserID)
	})
	reqZh := withURLParam(newRequest("GET", "/api/attachments/"+id+"/office-config", nil), "id", id)
	wZh := httptest.NewRecorder()
	testHandler.GetOfficeConfig(wZh, reqZh)
	if wZh.Code != http.StatusOK {
		t.Fatalf("zh user: expected 200, got %d: %s", wZh.Code, wZh.Body.String())
	}
	var respZh struct {
		Config map[string]any `json:"config"`
	}
	if err := json.Unmarshal(wZh.Body.Bytes(), &respZh); err != nil {
		t.Fatalf("zh decode: %v", err)
	}
	ecZh, _ := respZh.Config["editorConfig"].(map[string]any)
	if ecZh["lang"] != "zh" {
		t.Errorf("zh user: editorConfig.lang = %v, want zh", ecZh["lang"])
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

	// Missing fetch secret → 503.
	testHandler.cfg.OnlyOfficeFetchSecret = ""
	reqMis2 := withURLParam(newRequest("GET", "/api/attachments/"+id+"/office-config", nil), "id", id)
	wMis2 := httptest.NewRecorder()
	testHandler.GetOfficeConfig(wMis2, reqMis2)
	if wMis2.Code != http.StatusServiceUnavailable {
		t.Errorf("missing fetch secret: expected 503, got %d", wMis2.Code)
	}
	testHandler.cfg.OnlyOfficeFetchSecret = "fetch-secret"

	// Missing fetch base URL AND public URL → 503.
	testHandler.cfg.OnlyOfficeFetchBaseURL = ""
	testHandler.cfg.PublicURL = ""
	reqMis3 := withURLParam(newRequest("GET", "/api/attachments/"+id+"/office-config", nil), "id", id)
	wMis3 := httptest.NewRecorder()
	testHandler.GetOfficeConfig(wMis3, reqMis3)
	if wMis3.Code != http.StatusServiceUnavailable {
		t.Errorf("missing fetch base+public URL: expected 503, got %d", wMis3.Code)
	}
	testHandler.cfg.OnlyOfficeFetchBaseURL = "https://api.example.com"
	testHandler.cfg.PublicURL = prev.PublicURL

	// Disabled → 404.
	testHandler.cfg.OnlyOfficeEnabled = false
	req3 := withURLParam(newRequest("GET", "/api/attachments/"+id+"/office-config", nil), "id", id)
	w3 := httptest.NewRecorder()
	testHandler.GetOfficeConfig(w3, req3)
	if w3.Code != http.StatusNotFound {
		t.Errorf("disabled: expected 404, got %d", w3.Code)
	}
}

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
	if cc := w.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store (public document bytes)", cc)
	}

	// Disabled → 404.
	testHandler.cfg.OnlyOfficeEnabled = false
	wDisabled := httptest.NewRecorder()
	testHandler.ServeOfficeContent(wDisabled, officeReq(good))
	if wDisabled.Code != http.StatusNotFound {
		t.Errorf("disabled: expected 404, got %d", wDisabled.Code)
	}
	testHandler.cfg.OnlyOfficeEnabled = true

	// Misconfigured (empty FetchSecret) → 503.
	testHandler.cfg.OnlyOfficeFetchSecret = ""
	wMisc := httptest.NewRecorder()
	testHandler.ServeOfficeContent(wMisc, officeReq(good))
	if wMisc.Code != http.StatusServiceUnavailable {
		t.Errorf("misconfigured: expected 503, got %d", wMisc.Code)
	}
	testHandler.cfg.OnlyOfficeFetchSecret = "fetch-secret"

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
