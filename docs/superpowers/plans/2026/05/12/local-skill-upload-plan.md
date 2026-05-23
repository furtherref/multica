# Local Skill Upload Implementation Plan

> Source design: `docs/superpowers/specs/2026/05/12/local-skill-upload-design.md`

## Goal

Add a fourth creation method to the Add Skill dialog — "Upload folder or zip" — so users can select one or more local skill directories or zip bundles, preview detected skills in the browser, and submit them to a dedicated batch-import API.

## Architecture

```
Browser                                     Server
┌─────────────────────────────┐           ┌──────────────────────────────┐
│ local-skill-upload.ts       │           │ skill_import_local.go        │
│ · path normalization        │           │ · path/size/count validation │
│ · SKILL.md discovery        │  POST     │ · null byte sanitization     │
│ · group by skill root       │ ───────>  │ · createSkillWithFiles       │
│ · file read + UTF-8 check   │ /import-  │ · duplicate name → skipped   │
│ · skip policy + preview     │  local    │ · structured per-item result │
│ · candidateToImportRequest  │           └──────────────────────────────┘
│                             │
│ local-skill-upload-panel.tsx │
│ · drag-drop / folder / zip  │
│ · single / multi preview    │
│ · editable name & desc      │
│ · batch select (max 16)     │
│ · import progress & summary │
└─────────────────────────────┘
```

## Module Boundaries

| Package | File | Responsibility |
|---|---|---|
| `packages/views/skills/utils/` | `local-skill-upload.ts` | Pure functions: path normalization, SKILL.md discovery, file grouping, limit checks, zip parsing, drag-drop reading |
| `packages/views/skills/components/` | `local-skill-upload-panel.tsx` | UI component: drop zone, preview, editing, import, result summary |
| `packages/views/skills/components/` | `create-skill-dialog.tsx` | Entry point: add `upload` method card |
| `packages/views/skills/lib/` | `origin.ts` | Add `uploaded_bundle` origin type |
| `packages/views/skills/components/` | `skill-columns.tsx`, `skill-detail-page.tsx` | Display uploaded_bundle source info |
| `packages/views/locales/` | `en/skills.json`, `zh-Hans/skills.json` | i18n strings |
| `packages/core/types/` | `agent.ts`, `index.ts` | Shared TS types |
| `packages/core/api/` | `client.ts`, `schemas.ts` | API method + Zod response schema |
| `server/internal/handler/` | `skill_import_local.go` | Handler: decode, validate, create, publish events |
| `server/cmd/server/` | `router.go` | Register `POST /api/skills/import-local` |

## Limit Alignment

| Parameter | Client Constant | Server Constant | Notes |
|---|---|---|---|
| Per-file size | `MAX_SKILL_FILE_BYTES` = 1 MiB | `maxLocalUploadedSkillFileBytes` = 1 MiB | Includes SKILL.md itself |
| Files per skill | `MAX_SKILL_FILES` = 128 | `maxLocalUploadedSkillFiles` = 128 | Excludes SKILL.md |
| Text per skill | `MAX_SKILL_ACCEPTED_BYTES` = 8 MiB | `maxLocalUploadedSkillTotalBytes` = 8 MiB | UTF-8 bytes |
| Skills per import | `MAX_LOCAL_SKILL_IMPORT_BATCH` = 16 | `maxLocalUploadedSkills` = 16 | |
| HTTP request body | — | `maxLocalUploadedSkillRequestBytes` = 32 MiB | Covers JSON escaping overhead |

## Security Boundaries

- **Path validation**: Client `normalizeUploadPath` rejects absolute paths and `..` traversal; server `normalizeLocalUploadedSkillFilePath` additionally rejects hidden files (`.` prefix), metadata files (Thumbs.db, desktop.ini), null bytes, Windows drive paths, and SKILL.md overwrites
- **UTF-8 validation**: Client uses `TextDecoder("utf-8", { fatal: true })`; non-UTF-8 files are skipped as `binary_file`
- **Null bytes**: Client detects and skips files containing `\0`; accepted text has null bytes stripped before submission; server calls `sanitizeNullBytes` on all fields
- **Zip safety**: Checks `uncompressedSize` before inflating; only inflates entries under detected skill roots; skips oversized entries instead of rejecting the entire zip
- **Request body limit**: `http.MaxBytesReader` truncates before JSON decode to bound memory allocation
- **File input reset**: Clears `<input>` value after selection so the same path can be re-selected

## Test Strategy

### Client Unit Tests (Vitest)

**`local-skill-upload.test.ts`** — 27 tests covering:
- Frontmatter parsing
- Single/multi skill root grouping
- Root-level skill bundle supporting file retention
- Source label composition
- Nested SKILL.md ownership
- Hidden/binary/oversized/traversal file skipping
- Non-UTF-8 file skipping
- Oversized SKILL.md surfaced as `unreadable_skill_md`
- UTF-8 byte counting (CJK multi-byte)
- File count/byte budget short-circuit (stops reading excess files)
- Zip path traversal rejection
- Zip oversized entry marking (no inflate)
- Zip oversized SKILL.md marker preservation
- Zip per-skill independent file counting
- Zip hidden files not consuming budget
- Zip entries outside detected roots ignored

**`local-skill-upload-panel.test.tsx`** — 7 tests covering:
- Missing SKILL.md error display
- Single skill preview and import
- Zip drag-and-drop import
- Batch selection UX beyond 16 skills
- Partial success dialog retention with result summary
- zh-Hans localization verification
- File input reset

**`create-skill-dialog.test.tsx`** — 2 tests covering:
- Upload method card presence
- Chinese title rendering

**`origin.test.ts`** — 1 test covering uploaded_bundle origin

**`client.test.ts`** — 3 tests covering:
- API endpoint path and method
- Response schema parsing
- Malformed response fallback

### Server Unit Tests (Go test)

**`skill_import_local_test.go`** — 14 tests covering:
- Valid skill creation with files
- Duplicate name skip + continue
- Path traversal rejection
- Single file size limit rejection
- SKILL.md size limit rejection
- SKILL.md path cleaning rejection
- Request body size rejection
- Large payload rejection before decode
- Batch size (>16 skills) rejection
- JSON-escaped content within decoded bundle limit
- Multi-skill JSON-escaped batch acceptance
- Hidden file path rejection
- Metadata file path rejection
- Backslash path traversal rejection

## Risks

1. **Drag-and-drop compatibility**: `webkitGetAsEntry` is a non-standard API; some browsers may not support directory traversal. The code includes a plain-file fallback path.
2. **Zip decompression memory**: Zips with many small files may allocate significant browser memory. Mitigated by pre-inflate size checks and per-root file count limits.
3. **JSON escaping overhead**: Files with many special characters expand significantly under `JSON.stringify`. The server request cap (32 MiB) provides sufficient headroom for the decoded limits (8 MiB × up to 16 skills).

## Acceptance Criteria

- [x] Add Skill dialog shows four creation methods (manual, URL, runtime, upload)
- [x] Folder picker imports a single skill
- [x] Zip file imports a single skill
- [x] Folder/zip imports multiple skills with select/deselect
- [x] Preview shows skipped files with reasons
- [x] Duplicate names reported as skipped without affecting other skills
- [x] Partial success keeps dialog open with result summary
- [x] Hidden, binary, and oversized files correctly skipped
- [x] Server-side validation on paths, sizes, and counts
- [x] API responses validated through Zod schema; malformed responses degrade safely
- [x] en and zh-Hans localization complete
- [x] All unit tests pass
