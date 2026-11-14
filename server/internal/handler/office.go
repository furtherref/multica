package handler

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5"
	"github.com/multica-ai/multica/server/internal/util"
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

// officeLang maps a Multica user locale (the SUPPORTED_LOCALES values in
// packages/core/i18n/types.ts: "en", "zh-Hans", "ko", "ja"; empty when the
// user has not chosen one) to the OnlyOffice editor `lang` code. Anything
// unknown or empty falls back to "en", matching the app's DEFAULT_LOCALE.
func officeLang(userLocale string) string {
	switch userLocale {
	case "zh-Hans":
		return "zh"
	case "ko":
		return "ko"
	case "ja":
		return "ja"
	default:
		return "en"
	}
}

// officeFetchToken mints an opaque token authorizing the Document Server to
// fetch exactly one attachment until exp. Format: "<expUnix>.<hexHMAC>" where
// HMAC = HMAC-SHA256(secret, attachmentID + "." + expUnix).
func officeFetchToken(attachmentID, secret string, exp time.Time) string {
	expStr := strconv.FormatInt(exp.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(attachmentID + "." + expStr))
	return expStr + "." + hex.EncodeToString(mac.Sum(nil))
}

// signOfficeConfig returns the HS256 JWT of the config payload. The config map
// passed in MUST NOT already contain a "token" field — OnlyOffice 7.1+ rejects
// a config whose embedded token signs over itself. Callers build the token-less
// config, call this, then assign the result to config["token"].
func signOfficeConfig(config map[string]any, secret string) (string, error) {
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims(config))
	return tok.SignedString([]byte(secret))
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

// officePreviewReady reports whether office preview will actually succeed:
// OnlyOffice enabled AND fully configured (secrets + Document Server URL), i.e.
// exactly when GetOfficeConfig would return 200. The public /api/config flag
// uses this so the web app hides the office-attachment preview Eye when a fork
// is deployed without (or with a misconfigured) OnlyOffice Document Server.
func (h *Handler) officePreviewReady() bool {
	return h.cfg.OnlyOfficeEnabled &&
		h.officeSecretsReady() &&
		h.cfg.OnlyOfficeDocumentServerPublicURL != ""
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

	// Editor UI language follows the user's saved Multica preference; defaults
	// to "en" when unset or unreadable (the preview must not fail over locale).
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	lang := "en"
	if user, err := h.Queries.GetUser(r.Context(), parseUUID(userID)); err == nil {
		lang = officeLang(user.Language.String)
	}

	attID := uuidToString(att.ID)
	token := officeFetchToken(attID, h.cfg.OnlyOfficeFetchSecret, time.Now().Add(officeFetchTokenTTL))

	base := h.cfg.OnlyOfficeFetchBaseURL
	if base == "" {
		base = h.cfg.PublicURL
	}
	fetchURL := strings.TrimRight(base, "/") + "/api/office/" + attID + "/content?token=" + url.QueryEscape(token)

	config := map[string]any{
		// Embedded read-only viewer. This is the Community-edition-safe way to
		// drop the File/View/Plugins menu bar: the per-tab `customization.layout`
		// API is a paid white-label feature of the Developer edition, ignored by
		// the open-source (AGPL) Document Server. The embedded viewer has no menu
		// bar at all, keeps the ONLYOFFICE branding (AGPL requirement), and is
		// purpose-built for previewing a document inline.
		"type": "embedded",
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
			"lang": lang,
			// No `user` object: the embedded viewer is single-user and view-only,
			// so it renders no user/avatar chip — passing user identity (and an
			// avatar URL) would only add an unused, user-controlled value to the
			// signed config. anonymous.request stays false so the "enter a name
			// for collaboration" prompt can never appear.
			"customization": map[string]any{
				"chat":      false,
				"comments":  false,
				"help":      false,
				"anonymous": map[string]any{"request": false},
			},
			// Embedded-viewer controls live under editorConfig.embedded — a
			// top-level `embedded` field is ignored by the Document Server.
			// autostart:"document" keeps presentations in the document view
			// instead of the slideshow player; toolbarDocked pins the slim
			// toolbar to the top. No share/embed/save URLs are supplied, so those
			// buttons stay hidden — Multica's modal owns download / full-screen /
			// close.
			"embedded": map[string]any{
				"autostart":     "document",
				"toolbarDocked": "top",
			},
		},
	}

	signed, err := signOfficeConfig(config, h.cfg.OnlyOfficeJWTSecret)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to sign office config")
		return
	}
	config["token"] = signed

	// The response embeds a short-lived signed JWT and an HMAC-token document
	// URL — never let an intermediary cache it.
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{
		"document_server_url": h.cfg.OnlyOfficeDocumentServerPublicURL,
		"config":              config,
	})
}

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
	}, jwt.WithoutClaimsValidation())
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
	// Public token URL streaming document bytes — no intermediary caching.
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", att.ContentType)
	w.Header().Set("Content-Length", strconv.FormatInt(att.SizeBytes, 10))
	io.Copy(w, reader) //nolint:errcheck
}
