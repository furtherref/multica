# OnlyOffice Document Preview — Design

Date: 2026-06-20
Status: Approved (design), pending implementation plan

## Goal

Add in-app **read-only preview** for office document attachments (Word / Excel /
PowerPoint and their OpenDocument / CSV siblings) by embedding the already-deployed
OnlyOffice Document Server's viewer. Scope is **web + desktop**. Mobile keeps its
current download behavior.

Deployed Document Server:

- Public (browser-facing): `https://weboffice.example.com`
- Internal (cluster service): `http://weboffice.middleware.svc.cluster.local`
- Document Server has `JWT_ENABLED=true`, `JWT_HEADER=Authorization`, a `JWT_SECRET`
  and a `SECURE_LINK_SECRET` (the latter is the DS-internal nginx asset protection and
  is **not** used by this integration).

## Decisions (locked)

1. **Approach: embed the live OnlyOffice viewer** via `DocsAPI.DocEditor` in
   `mode: "view"`. Not server-side convert-to-PDF. Highest fidelity for multi-sheet
   Excel and PowerPoint, and matches the `JWT_ENABLED` config the team deployed.
2. **Read-only.** No callback endpoint, no save / version / conflict logic. Attachments
   are immutable in this system, so read-only is the natural fit.
3. **Platforms: web + desktop**, sharing one `packages/views` component.
4. **Config is signed by the backend.** The OnlyOffice JWT secret never reaches the
   browser; the frontend only ever receives an already-signed config.
5. **Document Server fetches the file from a dedicated backend endpoint** authorized by
   a short-lived HMAC token. Storage-backend agnostic (OSS / local disk both work); does
   not depend on the cluster being able to reach OSS public endpoints.
6. **CSV is part of the office set** (`documentType: "cell"`), not the existing text
   preview path. Office detection takes priority over text for CSV.
7. **The fetch token uses its own dedicated secret** (`ONLYOFFICE_FETCH_SECRET`),
   independent of the OnlyOffice JWT secret.
8. **`document.url` points at the backend's PUBLIC URL** (`https://multica.example.com`),
   not an internal ClusterIP / `.svc.cluster.local` address. The deployed Document Server
   (`documentserver:9.4.0`) ships with `request-filtering-agent.allowPrivateIPAddress=false`
   by default (SSRF protection), so it refuses to fetch private/reserved-range addresses.
   The verified-reachable public backend host is used instead. The dedicated-endpoint +
   HMAC-token design is unchanged; only the base URL differs.

## Data flow

```
Browser (web/desktop)                Backend (in cluster)              OnlyOffice DS
   |                                     |                          public:  weboffice.example.com
   |                                     |                          internal: weboffice...svc.cluster.local
   | 1. click Eye on a .docx             |                                |
   |-- GET /api/attachments/{id}/office-config -->|                       |
   |                                     | auth: workspace membership     |
   |                                     | gate: is office type           |
   |                                     | mint short-lived HMAC fetch-token
   |                                     | build config + HS256-sign config.token
   |<-- { documentServerUrl, config } ---|                                |
   |                                     |                                |
   | 2. load weboffice.../web-apps/apps/api/documents/api.js                   |
   | 3. new DocsAPI.DocEditor(div, config) --------------------------->   | 4. DS verifies config.token (JWT)
   |                                     |<-- 5. GET https://multica.example.com/api/office/{id}/content?token=… --| (DS server-side fetch, public URL)
   |                                     | public route (outside user-auth); verify HMAC token + expiry |
   |                                     | stream bytes from storage ------------------>|
   |<----------- 6. read-only document rendered in iframe ----------------------------- |
```

The browser only talks to the **public** Document Server (loads `api.js`, renders the
iframe). The file bytes flow **server-to-server**: the Document Server pulls them from our
backend's dedicated fetch endpoint at its **public** URL (the DS blocks private-IP
addresses, so the cluster-internal service address can't be used — see decision 8). Bytes
never transit the browser, and the path does not depend on OSS reachability from the
cluster.

## Backend (Go)

New file: `server/internal/handler/office.go`. Two endpoints registered in
`server/cmd/server/router.go`.

### `GET /api/attachments/{id}/office-config`

- Mounted inside the existing **authenticated + workspace-member middleware group** (the
  same group as `GET /api/attachments/{id}`, router.go:830). That middleware performs the
  workspace-membership check; the request reaches the handler only for members.
- Resolves the attachment via the existing `loadAttachmentForRequest()` loader. Note that
  this loader is **not** itself a membership verifier — it scopes a `GetAttachment` query
  by `workspace_id` (from the request) + `attachment_id` and 404s on miss. The actual
  authorization boundary is the surrounding middleware.
- Returns `404` when `ONLYOFFICE_ENABLED` is false; `400` when the attachment is not an
  office type.
- Mints a fetch token: `HMAC-SHA256(ONLYOFFICE_FETCH_SECRET, attachmentId + "." + exp)`,
  TTL ≈ 5 minutes (enough for the DS to fetch).
- Builds the **token-less** config object (below), HS256-signs that payload with the
  OnlyOffice JWT secret, then assigns the resulting JWT to `config.token`. Order matters:
  the signed payload must NOT already contain a `token` field. OnlyOffice 7.1+ validates
  the browser config signature strictly.
- Response body: `{ "documentServerUrl": "<public URL>", "config": { … } }`.

### `GET /api/office/{id}/content?token=...`

- **Registered as a PUBLIC route, OUTSIDE the user-auth middleware group.** The Document
  Server fetches this server-to-server and, because the deployment has
  `JWT_HEADER=Authorization` with request out-going enabled, it sends its **own**
  `Authorization: Bearer <onlyoffice-jwt>` header. That header must never reach Multica's
  user-auth middleware (it would be misread as a user token and rejected). So this route
  lives in the unauthenticated section of the router (alongside `/api/attachments/{id}/download`
  registration style, but with no user session requirement).
- **No user session** — authorized by the HMAC token in the query string.
- Validates token signature, expiry, and that the embedded `id` matches the path `id`.
- **Defense-in-depth (required):** also verifies the incoming OnlyOffice `Authorization`
  JWT signature against `ONLYOFFICE_JWT_SECRET` (signature only, not specific claims, to
  stay robust across DS versions). Even if the query token leaks via ingress logs, a caller
  without the JWT secret cannot forge this header. The deployment has `JWT_ENABLED=true`, so
  the DS always sends it, and the integration already hard-depends on JWT being on.
- Loads the attachment via `GetAttachmentByIDOnly`, streams bytes from
  `h.Storage.GetReader()` with the correct `Content-Type`.
- The token authorizes exactly one attachment for a short window. Enforces a max preview
  size (≈ 50 MB) to bound abuse.

### Config object

```jsonc
{
  "document": {
    "fileType": "docx",            // derived from filename extension
    "key": "<attachment.id>",      // attachments are immutable -> stable cache key
    "title": "季度报告.docx",
    "url": "https://multica.example.com/api/office/<id>/content?token=…",
    "permissions": { "edit": false, "download": false, "print": true }
  },
  "documentType": "word",          // word | cell | slide
  "editorConfig": {
    "mode": "view",
    "lang": "<from user.language>",  // en | zh | ko | ja, default en
    "customization": { "chat": false, "comments": false, "help": false }
  },
  "token": "<HS256 JWT of the whole object above>"
}
```

`document.key` uses `attachment.id` (a UUID, 36 chars, within OnlyOffice's 128-char limit
and allowed charset). Safe because attachment content never changes.

`documentType` mapping:

- `word` ← `doc`, `docx`, `odt`, `rtf`
- `cell` ← `xls`, `xlsx`, `ods`, `csv`
- `slide` ← `ppt`, `pptx`, `odp`

### New configuration (env vars → `handler.Config`)

| Env | Example | Purpose |
|---|---|---|
| `ONLYOFFICE_ENABLED` | `true` | Master switch |
| `ONLYOFFICE_DOCUMENT_SERVER_PUBLIC_URL` | `https://weboffice.example.com` | Frontend loads `api.js` / renders iframe |
| `ONLYOFFICE_JWT_SECRET` | (= deployment's `JWT_SECRET`) | Signs the config |
| `ONLYOFFICE_FETCH_SECRET` | (new, independent secret) | Signs/validates the fetch token |
| `ONLYOFFICE_FETCH_BASE_URL` | `https://multica.example.com` | Base for `document.url` that the DS fetches. **Must be a PUBLIC, non-private-IP host** — the DS blocks private/reserved addresses by default, so a `.svc.cluster.local` / ClusterIP value would fail. Falls back to existing `PublicURL` if unset. |

`documentServerInternalUrl` and `SECURE_LINK_SECRET` are **not consumed** by this
read-only embed integration (they'd matter only if we later call the DS conversion /
command service ourselves). Leave them configured for the future but do not wire them now.

## Frontend (packages/views, shared by web + desktop)

- **`packages/views/editor/utils/preview.ts`**: add `PreviewKind = "office"`. Detect the
  office extensions (`doc/docx/odt/rtf/xls/xlsx/ods/csv/ppt/pptx/odp`) and corresponding
  OOXML/ODF mime types. **Office detection runs before markdown/html/text**, so `csv`
  resolves to `office`, not `text`. Remove `csv` from the text-preview set (or rely on
  office-first ordering) so there is one unambiguous answer.
- **New component `packages/views/editor/office-attachment-preview.tsx`**, rendered inside
  the existing `AttachmentPreviewModal` for the `office` kind:
  1. `api.getOfficeConfig(id)` → `{ documentServerUrl, config }`.
  2. Dynamically inject `<documentServerUrl>/web-apps/apps/api/documents/api.js`
     (loaded once globally, with concurrency de-dup).
  3. `new window.DocsAPI.DocEditor(placeholderId, config)`.
  4. On unmount, call `editor.destroyEditor()`.
- **Inline rendering**: office attachments render as `AttachmentCard` + Eye button (same
  as PDF). Clicking opens the modal. No heavy inline embedding.
- **API client**: add `getOfficeConfig(id)` in `packages/core/api/client.ts`. Per the
  **API Response Compatibility** convention, add a zod schema in
  `packages/core/api/schemas.ts` consumed via `parseWithFallback`, plus a test feeding a
  malformed response through it.
- **Capability gating + graceful degradation**: the public `GET /api/config` endpoint
  exposes `office_preview_enabled` — true only when OnlyOffice is enabled AND fully
  configured (secrets + DS URL), i.e. exactly when `office-config` would return 200. It
  flows into the shared config store (`officePreviewEnabled`) and `AttachmentCard` hides
  the office preview Eye when it's false. Old servers / forks deployed without OnlyOffice
  omit the field → store defaults to false → no Eye, so end users never see a broken
  preview affordance. The click-time fallback (config 404/`api.js` failure → download
  card, never a white-screen) stays as the safety net for any path that still opens the
  modal.

## Content Security Policy

The OnlyOffice editor loads in the **frontend document** (the Next.js web page / the
Electron renderer), so the CSP that governs it is the frontend's — **not** the Go backend
CSP. Concretely, in this codebase:

- `server/internal/middleware/csp.go` (mounted at router.go:406) sets a strict CSP, but
  **only on Go backend responses** (`/api/*`, file streams). Those responses do not host
  the editor. **Changing csp.go does not unblock the editor** — it is the wrong layer.
- `apps/web` currently sets **no CSP** at all (no `headers()` in `next.config.ts`, no
  middleware CSP). The Electron renderer (`apps/desktop`) has no `<meta>` CSP and runs
  with `webSecurity: false`. The Helm `ingress.yaml` injects no CSP either.

So with the code as-is, nothing blocks the editor. Production edge/ingress was checked
live and **no CSP on the HTML document blocks the Document Server origin** — the editor
loads without any CSP change. **Resolved: no CSP work is required for this integration.**

(For reference, had an HTML CSP been present, it would have needed `https://weboffice.example.com`
allowed in `script-src` (loads `api.js`), `frame-src` / `child-src` (the editor iframe),
and `connect-src` including `wss:` (editor XHR + WebSocket) — fixed at the frontend/edge,
not in `csp.go`.)

## Deployment (Helm)

The new settings must be wired into the chart (`deploy/helm/multica/`):

- **ConfigMap** (`templates/configmap.yaml`): `ONLYOFFICE_ENABLED`,
  `ONLYOFFICE_DOCUMENT_SERVER_PUBLIC_URL`, `ONLYOFFICE_FETCH_BASE_URL`.
- **Secret**: `ONLYOFFICE_JWT_SECRET` (must equal the Document Server's `JWT_SECRET`) and
  `ONLYOFFICE_FETCH_SECRET` (new, independent).
- Expose corresponding keys in `values.yaml` so they are configurable per environment.
- The backend Deployment must reference the new ConfigMap keys / Secret keys as env vars.

## Security

- The OnlyOffice JWT secret stays server-side; the browser only ever receives a signed
  config.
- **Fail closed on misconfiguration:** if `ONLYOFFICE_ENABLED=true` but a required secret
  or URL is missing (JWT secret, fetch secret, DS public URL, or fetch base), both endpoints
  return `503` and mint nothing. Signing a config or HMAC with an empty secret would be
  forgeable, so "enabled but a secret is missing" must never silently produce signed-with-""
  output.
- Fetch token: HMAC + short TTL + bound to a single `attachmentId`; minimal exposure
  window. Strict expiry check; must not be usable for any other attachment.
- **Token-in-URL logging risk**: the fetch token rides in the query string (OnlyOffice
  fetches `document.url` with a plain GET, so a header is not an option). Ingress / nginx
  access logs may record it. Mitigated by the short TTL; the token grants read of one
  attachment for ~5 minutes only. Note this explicitly as an accepted residual risk.
- Fetch endpoint enforces a max preview size (≈ 50 MB).
- Reuse existing IDOR protection (404, not 403, on access denial).
- **`permissions.print`**: confirmed allowed — the config sets `print: true`. Read-only
  preview still permits the user to print/export-to-print from the viewer.

## Pre-launch validation

- **Chinese fonts**: the Document Server container must have CJK fonts registered in its
  fontconfig, or Chinese Word/Excel/PPT will render with tofu/substitute glyphs. Before
  launch, preview a representative Chinese document; if glyphs are wrong, install/register
  CJK fonts in the DS image and regenerate the OnlyOffice font cache.

## Testing

- **Go**: config endpoint (auth; type gating; the signed JWT verifies under the same
  secret; 404 when disabled). Fetch endpoint (valid HMAC; expiry rejection; streams
  bytes; rejects cross-attachment token reuse).
- **TS**: `getPreviewKind` returns `office` for the new types (including `csv`); schema
  feeds a malformed response through `parseWithFallback`; component test mocks
  `window.DocsAPI` and asserts load → instantiate → `destroyEditor` on unmount.
- **E2E**: requires a live Document Server; out of scope for this phase.

## Phasing

- **P1 (this phase)**: everything above — Word/Excel/PPT/CSV read-only preview on web +
  desktop.
- **Later (optional)**: online editing (needs a callback write-back endpoint), mobile
  WebView embedding, server-side convert-to-PDF thumbnails.
