# Issue Template Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add workspace-level `Issue模板` management and let users apply an issue template while manually creating an issue.

**Architecture:** Add a backend `issue_template` resource parallel to `skill`, expose typed core API/query/mutation helpers, build a Skill-style list/detail UI, add a sidebar route, and integrate template application into the manual create issue modal. The template only stores and applies template name, issue title, issue content, source metadata, creator, and timestamps.

**Tech Stack:** Go/chi/pgx/sqlc backend, PostgreSQL migrations, TypeScript, React, TanStack Query/Table, Next app routes, Vitest/Testing Library, existing Multica UI components.

---

## File Structure

Backend:

- Create `server/migrations/083_issue_templates.up.sql` and `server/migrations/083_issue_templates.down.sql`: create/drop `issue_template`.
- Create `server/pkg/db/queries/issue_template.sql`: sqlc CRUD queries.
- Modify generated sqlc files after running the repo's generator.
- Create `server/internal/handler/issue_template.go`: request/response types, validation, permissions, CRUD handlers.
- Modify `server/cmd/server/router.go`: register `/api/issue-templates`.
- Create `server/internal/handler/issue_template_test.go`: CRUD, validation, workspace scoping, permissions.

Core/shared frontend:

- Create `packages/core/types/issue-template.ts`: issue template API types.
- Modify `packages/core/types/index.ts`: export issue template types.
- Modify `packages/core/api/client.ts`: add issue template client methods.
- Create `packages/core/issue-templates/queries.ts`: query keys/options.
- Create `packages/core/issue-templates/mutations.ts`: create/update/delete mutations and invalidation.
- Modify `packages/core/paths/paths.ts`, `packages/core/paths/paths.test.ts`, `packages/core/paths/consistency.test.ts`, and `packages/core/paths/reserved-slugs.ts`: add `issueTemplates` and `issueTemplateDetail`.

Views/app:

- Modify `packages/views/layout/app-sidebar.tsx`: add `Issue模板` after Skill.
- Modify `packages/views/locales/en/layout.json`, `packages/views/locales/zh-Hans/layout.json`, `packages/views/locales/en/search.json`, `packages/views/locales/zh-Hans/search.json`: add sidebar/search labels.
- Create `packages/views/issue-templates/index.ts`: public exports.
- Create `packages/views/issue-templates/lib/origin.ts`: read manual origin metadata.
- Create `packages/views/issue-templates/components/issue-template-columns.tsx`: table columns.
- Create `packages/views/issue-templates/components/issue-templates-page.tsx`: list page.
- Create `packages/views/issue-templates/components/create-issue-template-dialog.tsx`: create dialog.
- Create `packages/views/issue-templates/components/issue-template-detail-page.tsx`: edit/delete detail page.
- Create `packages/views/locales/en/issue-templates.json` and `packages/views/locales/zh-Hans/issue-templates.json`: page strings.
- Modify `packages/views/locales/index.ts`: register namespace.
- Create Next routes:
  - `apps/web/app/[workspaceSlug]/(dashboard)/issue-templates/page.tsx`
  - `apps/web/app/[workspaceSlug]/(dashboard)/issue-templates/[id]/page.tsx`
- Modify `packages/views/modals/create-issue.tsx`: template selector entry, apply flow, overwrite confirmation.
- Modify `packages/views/modals/create-issue.test.tsx`: template apply coverage.

## Task 1: Backend Storage And Queries

**Files:**
- Create: `server/migrations/083_issue_templates.up.sql`
- Create: `server/migrations/083_issue_templates.down.sql`
- Create: `server/pkg/db/queries/issue_template.sql`
- Generated after sqlc: `server/pkg/db/generated/issue_template.sql.go`
- Generated after sqlc: `server/pkg/db/generated/models.go`

- [ ] **Step 1: Add a failing query compilation target**

Create `server/pkg/db/queries/issue_template.sql` with:

```sql
-- Issue Template CRUD

-- name: ListIssueTemplateSummariesByWorkspace :many
SELECT id, workspace_id, name, issue_title, issue_content, config, created_by, created_at, updated_at
FROM issue_template
WHERE workspace_id = $1
ORDER BY name ASC;

-- name: GetIssueTemplateInWorkspace :one
SELECT *
FROM issue_template
WHERE id = $1 AND workspace_id = $2;

-- name: CreateIssueTemplate :one
INSERT INTO issue_template (workspace_id, name, issue_title, issue_content, config, created_by)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: UpdateIssueTemplate :one
UPDATE issue_template SET
    name = COALESCE(sqlc.narg('name'), name),
    issue_title = COALESCE(sqlc.narg('issue_title'), issue_title),
    issue_content = COALESCE(sqlc.narg('issue_content'), issue_content),
    config = COALESCE(sqlc.narg('config'), config),
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteIssueTemplate :exec
DELETE FROM issue_template WHERE id = $1;
```

- [ ] **Step 2: Add the migration**

Create `server/migrations/083_issue_templates.up.sql`:

```sql
CREATE TABLE issue_template (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    issue_title TEXT NOT NULL,
    issue_content TEXT NOT NULL DEFAULT '',
    config JSONB NOT NULL DEFAULT '{}',
    created_by UUID REFERENCES "user"(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(workspace_id, name)
);

CREATE INDEX idx_issue_template_workspace ON issue_template(workspace_id);
```

Create `server/migrations/083_issue_templates.down.sql`:

```sql
DROP TABLE IF EXISTS issue_template;
```

- [ ] **Step 3: Run sqlc generation**

Run the repo's existing generation command. First inspect `server/sqlc.yaml` or package scripts, then run the matching command, expected to be one of:

```bash
pnpm --filter @multica/server sqlc
```

or:

```bash
cd server && sqlc generate
```

Expected: generated `server/pkg/db/generated/issue_template.sql.go` and an `IssueTemplate` model in `server/pkg/db/generated/models.go`.

- [ ] **Step 4: Commit backend storage**

```bash
git add server/migrations server/pkg/db/queries/issue_template.sql server/pkg/db/generated
git commit -m "feat: add issue template storage"
```

## Task 2: Backend Handlers And Tests

**Files:**
- Create: `server/internal/handler/issue_template.go`
- Create: `server/internal/handler/issue_template_test.go`
- Modify: `server/cmd/server/router.go`

- [ ] **Step 1: Write handler tests first**

Create `server/internal/handler/issue_template_test.go` with tests for:

```go
func TestIssueTemplateCRUD(t *testing.T) {
    createReq := map[string]any{
        "name": "Bug report",
        "issue_title": "Investigate {{area}} bug",
        "issue_content": "## Context\n\n## Steps\n",
    }
    w := httptest.NewRecorder()
    testHandler.CreateIssueTemplate(w, newRequest("POST", "/api/issue-templates?workspace_id="+testWorkspaceID, createReq))
    if w.Code != http.StatusCreated {
        t.Fatalf("CreateIssueTemplate status = %d body=%s", w.Code, w.Body.String())
    }

    var created map[string]any
    if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
        t.Fatal(err)
    }
    id := created["id"].(string)
    if created["name"] != "Bug report" || created["issue_title"] != "Investigate {{area}} bug" {
        t.Fatalf("created response mismatch: %#v", created)
    }

    w = httptest.NewRecorder()
    testHandler.ListIssueTemplates(w, newRequest("GET", "/api/issue-templates?workspace_id="+testWorkspaceID, nil))
    if w.Code != http.StatusOK {
        t.Fatalf("ListIssueTemplates status = %d body=%s", w.Code, w.Body.String())
    }

    w = httptest.NewRecorder()
    testHandler.GetIssueTemplate(w, newRequest("GET", "/api/issue-templates/"+id+"?workspace_id="+testWorkspaceID, nil))
    if w.Code != http.StatusOK {
        t.Fatalf("GetIssueTemplate status = %d body=%s", w.Code, w.Body.String())
    }

    w = httptest.NewRecorder()
    testHandler.UpdateIssueTemplate(w, newRequest("PUT", "/api/issue-templates/"+id+"?workspace_id="+testWorkspaceID, map[string]any{
        "name": "Bug triage",
        "issue_title": "Triage bug",
        "issue_content": "Updated body",
    }))
    if w.Code != http.StatusOK {
        t.Fatalf("UpdateIssueTemplate status = %d body=%s", w.Code, w.Body.String())
    }

    w = httptest.NewRecorder()
    testHandler.DeleteIssueTemplate(w, newRequest("DELETE", "/api/issue-templates/"+id+"?workspace_id="+testWorkspaceID, nil))
    if w.Code != http.StatusNoContent {
        t.Fatalf("DeleteIssueTemplate status = %d body=%s", w.Code, w.Body.String())
    }
}
```

Add tests that assert missing `name` and missing `issue_title` return `400`.

- [ ] **Step 2: Run tests and verify failure**

Run:

```bash
go test ./server/internal/handler -run 'TestIssueTemplate'
```

Expected: compile failure because handlers do not exist.

- [ ] **Step 3: Implement handlers**

Create `server/internal/handler/issue_template.go` with:

```go
package handler

import (
    "encoding/json"
    "net/http"
    "strings"

    "github.com/go-chi/chi/v5"
    "github.com/jackc/pgx/v5/pgtype"
    db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type IssueTemplateResponse struct {
    ID           string  `json:"id"`
    WorkspaceID  string  `json:"workspace_id"`
    Name         string  `json:"name"`
    IssueTitle   string  `json:"issue_title"`
    IssueContent string  `json:"issue_content"`
    Config       any     `json:"config"`
    CreatedBy    *string `json:"created_by"`
    CreatedAt    string  `json:"created_at"`
    UpdatedAt    string  `json:"updated_at"`
}

type CreateIssueTemplateRequest struct {
    Name         string `json:"name"`
    IssueTitle   string `json:"issue_title"`
    IssueContent string `json:"issue_content"`
    Config       any    `json:"config"`
}

type UpdateIssueTemplateRequest struct {
    Name         *string `json:"name"`
    IssueTitle   *string `json:"issue_title"`
    IssueContent *string `json:"issue_content"`
    Config       any     `json:"config"`
}

func issueTemplateToResponse(t db.IssueTemplate) IssueTemplateResponse {
    return IssueTemplateResponse{
        ID:           uuidToString(t.ID),
        WorkspaceID:  uuidToString(t.WorkspaceID),
        Name:         t.Name,
        IssueTitle:   t.IssueTitle,
        IssueContent: t.IssueContent,
        Config:       decodeSkillConfig(t.Config),
        CreatedBy:    uuidToPtr(t.CreatedBy),
        CreatedAt:    timestampToString(t.CreatedAt),
        UpdatedAt:    timestampToString(t.UpdatedAt),
    }
}
```

Then implement `ListIssueTemplates`, `GetIssueTemplate`, `CreateIssueTemplate`, `UpdateIssueTemplate`, and `DeleteIssueTemplate` following `skill.go` patterns:

- Use `h.resolveWorkspaceID(r)`.
- Use `requireUserID` on create.
- Use `loadIssueTemplateForUser`.
- Use `canManageIssueTemplate`, same rule as skill: creator or owner/admin can update/delete.
- Trim `name` and `issue_title`, reject empty.
- Sanitize null bytes using `sanitizeNullBytes`.
- Marshal nil config to `{}`.
- Use `isUniqueViolation` to return conflict for duplicate template names.

- [ ] **Step 4: Register routes**

In `server/cmd/server/router.go`, near `/api/skills`, add:

```go
r.Route("/api/issue-templates", func(r chi.Router) {
    r.Get("/", h.ListIssueTemplates)
    r.Post("/", h.CreateIssueTemplate)
    r.Route("/{id}", func(r chi.Router) {
        r.Get("/", h.GetIssueTemplate)
        r.Put("/", h.UpdateIssueTemplate)
        r.Delete("/", h.DeleteIssueTemplate)
    })
})
```

- [ ] **Step 5: Run backend tests**

```bash
go test ./server/internal/handler -run 'TestIssueTemplate'
```

Expected: PASS.

- [ ] **Step 6: Commit backend handlers**

```bash
git add server/internal/handler/issue_template.go server/internal/handler/issue_template_test.go server/cmd/server/router.go
git commit -m "feat: add issue template API"
```

## Task 3: Core TypeScript API, Paths, And Query Helpers

**Files:**
- Create: `packages/core/types/issue-template.ts`
- Modify: `packages/core/types/index.ts`
- Modify: `packages/core/api/client.ts`
- Create: `packages/core/issue-templates/queries.ts`
- Create: `packages/core/issue-templates/mutations.ts`
- Modify: `packages/core/paths/paths.ts`
- Modify: `packages/core/paths/paths.test.ts`
- Modify: `packages/core/paths/consistency.test.ts`
- Modify: `packages/core/paths/reserved-slugs.ts`

- [ ] **Step 1: Write path tests first**

In `packages/core/paths/paths.test.ts`, add:

```ts
expect(ws.issueTemplates()).toBe("/acme/issue-templates");
expect(ws.issueTemplateDetail("tpl_123")).toBe("/acme/issue-templates/tpl_123");
```

In `packages/core/paths/consistency.test.ts`, include `issue-templates` in reserved/dashboard route expectations using the existing test pattern.

- [ ] **Step 2: Run path tests and verify failure**

```bash
pnpm --filter @multica/core test -- paths
```

Expected: FAIL because the path methods do not exist.

- [ ] **Step 3: Add types**

Create `packages/core/types/issue-template.ts`:

```ts
export interface IssueTemplateSummary {
  id: string;
  workspace_id: string;
  name: string;
  issue_title: string;
  issue_content: string;
  config: Record<string, unknown>;
  created_by: string | null;
  created_at: string;
  updated_at: string;
}

export interface IssueTemplate extends IssueTemplateSummary {}

export interface CreateIssueTemplateRequest {
  name: string;
  issue_title: string;
  issue_content?: string;
  config?: Record<string, unknown>;
}

export interface UpdateIssueTemplateRequest {
  name?: string;
  issue_title?: string;
  issue_content?: string;
  config?: Record<string, unknown>;
}
```

Export these from `packages/core/types/index.ts`.

- [ ] **Step 4: Add client methods**

In `packages/core/api/client.ts`, import the new request/response types and add:

```ts
async listIssueTemplates(): Promise<IssueTemplateSummary[]> {
  return this.fetch("/api/issue-templates");
}

async getIssueTemplate(id: string): Promise<IssueTemplate> {
  return this.fetch(`/api/issue-templates/${id}`);
}

async createIssueTemplate(data: CreateIssueTemplateRequest): Promise<IssueTemplate> {
  return this.fetch("/api/issue-templates", {
    method: "POST",
    body: JSON.stringify(data),
  });
}

async updateIssueTemplate(id: string, data: UpdateIssueTemplateRequest): Promise<IssueTemplate> {
  return this.fetch(`/api/issue-templates/${id}`, {
    method: "PUT",
    body: JSON.stringify(data),
  });
}

async deleteIssueTemplate(id: string): Promise<void> {
  await this.fetch(`/api/issue-templates/${id}`, { method: "DELETE" });
}
```

- [ ] **Step 5: Add query and mutation helpers**

Create `packages/core/issue-templates/queries.ts`:

```ts
import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";

export const issueTemplateKeys = {
  all: (workspaceId: string | null | undefined) => ["workspaces", workspaceId, "issue-templates"] as const,
  detail: (workspaceId: string | null | undefined, id: string) =>
    ["workspaces", workspaceId, "issue-templates", id] as const,
};

export function issueTemplateListOptions(workspaceId: string | null | undefined) {
  return queryOptions({
    queryKey: issueTemplateKeys.all(workspaceId),
    queryFn: () => api.listIssueTemplates(),
    enabled: !!workspaceId,
  });
}

export function issueTemplateDetailOptions(workspaceId: string | null | undefined, id: string) {
  return queryOptions({
    queryKey: issueTemplateKeys.detail(workspaceId, id),
    queryFn: () => api.getIssueTemplate(id),
    enabled: !!workspaceId && !!id,
  });
}
```

Create `packages/core/issue-templates/mutations.ts` with `useCreateIssueTemplate`, `useUpdateIssueTemplate`, and `useDeleteIssueTemplate` using `useMutation`, `useQueryClient`, and invalidating `issueTemplateKeys.all(workspaceId)`.

- [ ] **Step 6: Add paths**

In `packages/core/paths/paths.ts`, add methods:

```ts
issueTemplates: () => `${ws}/issue-templates`,
issueTemplateDetail: (id: string) => `${ws}/issue-templates/${encode(id)}`,
```

Add `"issue-templates"` to `reserved-slugs.ts`.

- [ ] **Step 7: Run core tests**

```bash
pnpm --filter @multica/core test -- paths
```

Expected: PASS.

- [ ] **Step 8: Commit core helpers**

```bash
git add packages/core
git commit -m "feat: add issue template core API"
```

## Task 4: Sidebar, Routes, Locales, And List Page

**Files:**
- Modify: `packages/views/layout/app-sidebar.tsx`
- Modify: `packages/views/layout/app-sidebar.test.tsx`
- Create: `packages/views/locales/en/issue-templates.json`
- Create: `packages/views/locales/zh-Hans/issue-templates.json`
- Modify: `packages/views/locales/index.ts`
- Modify: `packages/views/locales/en/layout.json`
- Modify: `packages/views/locales/zh-Hans/layout.json`
- Create: `packages/views/issue-templates/index.ts`
- Create: `packages/views/issue-templates/lib/origin.ts`
- Create: `packages/views/issue-templates/components/issue-template-columns.tsx`
- Create: `packages/views/issue-templates/components/issue-templates-page.tsx`
- Create: `apps/web/app/[workspaceSlug]/(dashboard)/issue-templates/page.tsx`

- [ ] **Step 1: Write sidebar test first**

Update `packages/views/layout/app-sidebar.test.tsx` mocks to include:

```ts
issueTemplates: () => "/acme/issue-templates",
```

Add an assertion that `Issue模板` appears after `Skill` and before settings in the rendered sidebar.

- [ ] **Step 2: Run sidebar test and verify failure**

```bash
pnpm --filter @multica/views test -- app-sidebar
```

Expected: FAIL because sidebar label/path does not exist.

- [ ] **Step 3: Add locale namespaces**

Create `packages/views/locales/zh-Hans/issue-templates.json`:

```json
{
  "page": {
    "title": "Issue模板",
    "create": "新建 Issue模板",
    "search_placeholder": "搜索 Issue模板...",
    "intro": "在工作区内共享可复用的 issue 标题和内容模板，创建 issue 时可以套用后再调整。",
    "empty_title": "还没有 Issue模板",
    "empty_body": "创建第一个模板，让重复 issue 更快开始。",
    "no_matches": "没有匹配的 Issue模板"
  },
  "table": {
    "name": "名称",
    "source": "来源 · 添加者",
    "updated": "更新时间",
    "source_manual": "手动创建",
    "by_creator": "由 {{name}} 创建",
    "no_content": "暂无内容"
  },
  "create_dialog": {
    "title": "新建 Issue模板",
    "name_label": "模板名称",
    "issue_title_label": "issue标题",
    "issue_content_label": "issue内容",
    "cancel": "取消",
    "submit": "创建",
    "toast_created": "Issue模板已创建",
    "toast_failed": "创建 Issue模板失败"
  }
}
```

Create `packages/views/locales/en/issue-templates.json` with equivalent English strings. Register both in `packages/views/locales/index.ts`.

- [ ] **Step 4: Add sidebar item**

In `packages/views/layout/app-sidebar.tsx`:

- Add `FileText` or `SquarePen` icon import from lucide if needed.
- Extend `NavKey` and `NavLabelKey` with `issueTemplates`.
- Add `{ key: "issueTemplates", labelKey: "issue_templates", icon: FileText }` after skills in `configureNav`.

In layout locale JSON files, add:

```json
"issue_templates": "Issue模板"
```

for Chinese and:

```json
"issue_templates": "Issue Templates"
```

for English.

- [ ] **Step 5: Build list page files**

Create `packages/views/issue-templates/index.ts`:

```ts
export { default as IssueTemplatesPage } from "./components/issue-templates-page";
```

Create a list page modeled on `SkillsPage`: use `issueTemplateListOptions`, member list options for creators, `useReactTable`, a search input, empty state, and `DataTable`. Implement columns in `issue-template-columns.tsx`:

- `name`: primary `template.name`, secondary `template.issue_title || template.issue_content`.
- `source`: manual source plus creator avatar/name, copied from Skill `SourceCell` pattern.
- `updated`: `timeAgo(template.updated_at)`.
- chevron.

- [ ] **Step 6: Add Next route**

Create `apps/web/app/[workspaceSlug]/(dashboard)/issue-templates/page.tsx`:

```tsx
import { IssueTemplatesPage } from "@multica/views/issue-templates";

export default function IssueTemplatesRoute() {
  return <IssueTemplatesPage />;
}
```

- [ ] **Step 7: Run views tests**

```bash
pnpm --filter @multica/views test -- app-sidebar
pnpm --filter @multica/views typecheck
```

Expected: PASS. If full typecheck reports pre-existing unrelated errors, capture the exact unrelated files before continuing.

- [ ] **Step 8: Commit sidebar/list page**

```bash
git add packages/views apps/web/app/[workspaceSlug]/(dashboard)/issue-templates packages/core/paths
git commit -m "feat: add issue template list page"
```

## Task 5: Create And Detail Issue Template UI

**Files:**
- Create: `packages/views/issue-templates/components/create-issue-template-dialog.tsx`
- Create: `packages/views/issue-templates/components/issue-template-detail-page.tsx`
- Modify: `packages/views/issue-templates/components/issue-templates-page.tsx`
- Create: `apps/web/app/[workspaceSlug]/(dashboard)/issue-templates/[id]/page.tsx`
- Modify: `packages/views/locales/en/issue-templates.json`
- Modify: `packages/views/locales/zh-Hans/issue-templates.json`

- [ ] **Step 1: Add route and dialog tests**

Create focused tests near issue template components that assert:

- Clicking `新建 Issue模板` opens a dialog.
- Submitting valid name/title/content calls `api.createIssueTemplate`.
- Detail page loads a template and calls update on save.
- Delete asks for confirmation and calls delete.

- [ ] **Step 2: Run tests and verify failure**

```bash
pnpm --filter @multica/views test -- issue-template
```

Expected: FAIL because components do not exist.

- [ ] **Step 3: Implement create dialog**

Build `CreateIssueTemplateDialog` using existing dialog/button/input patterns:

- Local state for `name`, `issueTitle`, `issueContent`.
- Disable submit unless `name.trim()` and `issueTitle.trim()` exist.
- On submit call `useCreateIssueTemplate`.
- On success toast and navigate to `paths.issueTemplateDetail(created.id)`.
- On failure toast.

- [ ] **Step 4: Implement detail page**

Build `IssueTemplateDetailPage({ templateId })`:

- Query `issueTemplateDetailOptions(wsId, templateId)`.
- Editable fields for template name, issue title, issue content.
- Save button calls `useUpdateIssueTemplate`.
- Delete button opens confirmation and calls `useDeleteIssueTemplate`.
- After delete navigate to `paths.issueTemplates()`.

- [ ] **Step 5: Add detail route**

Create `apps/web/app/[workspaceSlug]/(dashboard)/issue-templates/[id]/page.tsx`:

```tsx
import { IssueTemplateDetailPage } from "@multica/views/issue-templates";

export default function IssueTemplateDetailRoute({
  params,
}: {
  params: { id: string };
}) {
  return <IssueTemplateDetailPage templateId={params.id} />;
}
```

- [ ] **Step 6: Run UI tests/typecheck**

```bash
pnpm --filter @multica/views test -- issue-template
pnpm --filter @multica/views typecheck
pnpm --filter @multica/web typecheck
```

Expected: PASS or only documented unrelated pre-existing failures.

- [ ] **Step 7: Commit create/detail UI**

```bash
git add packages/views/issue-templates apps/web/app/[workspaceSlug]/(dashboard)/issue-templates packages/views/locales
git commit -m "feat: add issue template editor"
```

## Task 6: Manual Create Issue Template Application

**Files:**
- Modify: `packages/views/modals/create-issue.tsx`
- Modify: `packages/views/modals/create-issue.test.tsx`
- Modify: `packages/views/locales/en/modals.json`
- Modify: `packages/views/locales/zh-Hans/modals.json`

- [ ] **Step 1: Write create modal tests first**

In `packages/views/modals/create-issue.test.tsx`, add mocks for issue template list/detail queries and tests:

```ts
it("applies an issue template to empty title and description", async () => {
  // render modal with project_id
  // click "Select template"
  // choose "Bug report"
  // expect title input to have issue_title
  // expect description textarea to have issue_content
});

it("confirms before overwriting existing title or description", async () => {
  // type existing title and description
  // choose template
  // expect overwrite confirmation
  // cancel keeps existing values
  // choose again and confirm overwrites values
});

it("does not change non-template issue fields when applying a template", async () => {
  // select project/status/priority through mocked controls or inspect mutation payload
  // apply template
  // submit
  // expect only title/description changed; project/status/priority remain as before
});
```

- [ ] **Step 2: Run modal tests and verify failure**

```bash
pnpm --filter @multica/views test -- create-issue
```

Expected: FAIL because template UI is missing.

- [ ] **Step 3: Add template picker UI**

In `ManualCreatePanel`:

- Query `issueTemplateListOptions(wsId)`.
- Add a toolbar button labeled from locale, e.g. `套用 Issue 模板`.
- Open a small dropdown/dialog listing templates by name and issue title.
- Empty state links/navigates to `paths.issueTemplates()`.

- [ ] **Step 4: Add apply logic**

Add helper logic:

```ts
const applyTemplate = async (templateId: string) => {
  const hasExisting = !!title.trim() || !!descEditorRef.current?.getMarkdown()?.trim();
  if (hasExisting) {
    setPendingTemplateId(templateId);
    setOverwriteConfirmOpen(true);
    return;
  }
  await applyTemplateNow(templateId);
};
```

`applyTemplateNow` should fetch detail with `api.getIssueTemplate(templateId)` or use a query client fetch, then:

- `updateTitle(template.issue_title)`
- set editor content to `template.issue_content`
- `setDraft({ title: template.issue_title, description: template.issue_content })`

If `ContentEditorRef` lacks a set method, extend the editor ref in `packages/views/editor` with a minimal `setMarkdown(markdown: string)` method and update tests accordingly.

- [ ] **Step 5: Add overwrite confirmation**

Use existing dialog/alert patterns. Copy:

- Title: `覆盖当前标题和内容？`
- Description: `套用模板会替换当前 issue 标题和内容。其他字段不会改变。`
- Cancel: `取消`
- Confirm: `覆盖`

On cancel, leave current fields unchanged. On confirm, call `applyTemplateNow(pendingTemplateId)`.

- [ ] **Step 6: Run modal tests/typecheck**

```bash
pnpm --filter @multica/views test -- create-issue
pnpm --filter @multica/views typecheck
```

Expected: PASS or only documented unrelated pre-existing failures.

- [ ] **Step 7: Commit create issue integration**

```bash
git add packages/views/modals packages/views/editor packages/views/locales
git commit -m "feat: apply issue templates when creating issues"
```

## Task 7: End-To-End Validation And Final Push

**Files:**
- No planned source changes unless validation finds defects.

- [ ] **Step 1: Run backend tests**

```bash
go test ./server/internal/handler -run 'TestIssueTemplate|TestListSkills'
```

Expected: PASS.

- [ ] **Step 2: Run frontend focused tests**

```bash
pnpm --filter @multica/core test -- paths
pnpm --filter @multica/views test -- 'app-sidebar|issue-template|create-issue'
```

Expected: PASS.

- [ ] **Step 3: Run typechecks**

```bash
pnpm --filter @multica/core typecheck
pnpm --filter @multica/views typecheck
pnpm --filter @multica/web typecheck
```

Expected: PASS. If existing unrelated failures remain, record exact files and commands.

- [ ] **Step 4: Inspect changed files**

```bash
git status --short
git diff --stat origin/agent/ai-gpt/98dde0bc...HEAD
```

Expected: only files for issue template feature are changed.

- [ ] **Step 5: Push branch**

```bash
git push
```

Expected: branch `agent/ai-gpt/98dde0bc` pushed.

- [ ] **Step 6: Post implementation summary**

Post a Multica issue comment using `--content-stdin` summarizing:

- Backend API/storage added.
- `Issue模板` page and sidebar entry added.
- Create issue modal can apply templates with overwrite confirmation.
- Tests run and any known unrelated failures.

Then move issue to the next requested status only after code review workflow completes.
