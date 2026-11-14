# Issue Template Design

## Goal

Add workspace-level issue templates so users can manage reusable issue title/content blueprints and apply one while creating a new issue.

## Confirmed Scope

This feature is the smaller product-scoped version of issue templates. It does not add a new issue status, template-specific issue IDs, duplicate workflows, or template-to-issue conversion rules.

Confirmed requirements:

- Add an `Issue模板` item to the left sidebar under the `配置` group.
- Place `Issue模板` after `Skill` and before `设置`.
- The page title is `Issue模板`.
- Issue templates are workspace-level records.
- Template fields are:
  - template name
  - issue title
  - issue content
  - source / creator
  - updated time
- The source / creator and updated time presentation should follow the Skill page table pattern.
- Creating an issue from a template copies only issue title and issue content.
- Applying a template never changes status, priority, assignee, project, due date, parent/child issues, attachments, or create-another settings.
- If the create issue form already has a title or content, applying a template first shows a confirmation dialog. Confirming overwrites title/content; cancelling keeps the current inputs.

## Current Context

The current create issue flow lives mainly in `packages/views/modals/create-issue.tsx`, with `CreateIssueDialog` in `packages/views/modals/create-issue-dialog.tsx`.

The sidebar configuration group is defined in `packages/views/layout/app-sidebar.tsx`. Current entries are runtimes, skills, and settings.

The Skill management experience provides the closest product and implementation reference:

- `packages/views/skills/components/skills-page.tsx`
- `packages/views/skills/components/skill-columns.tsx`
- `packages/views/skills/components/create-skill-dialog.tsx`
- `packages/core/api/client.ts` skill methods
- `packages/core/types/agent.ts` skill types

## Recommended Approach

Use a first-class workspace-level `issue_templates` model and a dedicated `Issue模板` management section. The page should mirror the Skill list/detail shape rather than embedding management into the create issue modal.

This keeps the issue creation flow focused while giving template content enough space to be edited comfortably.

## Information Architecture

Add a new sidebar destination:

- Group: `配置`
- Order: `运行时`, `Skill`, `Issue模板`, `设置`
- Label: `Issue模板`
- Page title: `Issue模板`

The route can use an English/internal path such as `/<workspace>/issue-templates`, while the visible label remains `Issue模板`.

The create issue modal gets a template selection entry. It consumes templates only; template creation and management stays on the `Issue模板` page.

## Data Model And API

Add a workspace-level `issue_templates` model. Each template belongs to one workspace.

Fields:

- `id`
- `workspace_id`
- `name`
- `issue_title`
- `issue_content`
- `config` or equivalent source metadata
- `created_by`
- `created_at`
- `updated_at`

Initial source behavior:

- First version only needs to display `手动创建`.
- Keep source metadata extensible, similar to Skill origin handling, so later imports or generated templates can be represented without changing the main table shape.

API behavior:

- List templates for the current workspace.
- Get template detail, including full issue content.
- Create template.
- Update template.
- Delete template.

The list endpoint should return enough fields for the table. The detail endpoint should return full `issue_content`.

Permissions:

- Workspace members can view templates.
- Create, edit, and delete permissions should align with the existing Skill permissions model unless implementation discovers a stronger existing workspace settings convention.

## Issue Template Page

The `Issue模板` page should reuse the Skill page density and table style.

Header:

- Title: `Issue模板`
- Count
- Short explanatory copy
- Primary action: `新建 Issue模板`

Table:

- Search supports template name, issue title, and issue content.
- Columns:
  - `名称`
  - `来源 · 添加者`
  - `更新时间`
  - right-side chevron
- The name cell shows template name as the primary line.
- The secondary line shows issue title or a short issue content excerpt.
- `来源 · 添加者` follows the Skill table pattern, with manual source and creator display.
- `更新时间` uses the same relative time style as Skill.

Empty state:

- Explain that templates can be reused when creating issues.
- Offer the same primary create action.

## Detail And Editing

Clicking a row opens an issue template detail page.

The detail page supports editing:

- Template name
- Issue title
- Issue content

Save behavior:

- Persist changes through the update API.
- Refresh `updated_at` in list/detail data after save.
- Show success and failure feedback consistent with existing Skill pages.

Delete behavior:

- Provide delete from the detail page.
- Show a confirmation dialog before deleting.
- After deletion, navigate back to the `Issue模板` list.

## Create Issue Integration

Add a template selection entry in the manual create issue modal.

Flow:

1. User opens the create issue modal.
2. User clicks the template entry.
3. Template selector lists available issue templates.
4. User selects a template.
5. If the current title or content is non-empty, show an overwrite confirmation.
6. If confirmed, fetch/apply the selected template details.
7. Set form title to `issue_title`.
8. Set editor content to `issue_content`.

Template application must not change:

- status
- priority
- assignee
- project
- due date
- parent issue
- child issues
- attachments
- create-another setting

If no templates exist, the selector should show an empty state and link or direct the user to the `Issue模板` page.

If loading or applying a template fails, show a toast and keep the create issue modal open.

## Error Handling

Backend validation:

- `name` is required and trimmed.
- `issue_title` is required and trimmed.
- `issue_content` can be empty unless existing issue creation constraints require otherwise.
- Requests must be scoped to the current workspace.
- Updating or deleting a missing template returns not found.

Frontend handling:

- List and detail pages show retryable load errors.
- Create, update, delete, and apply failures show toast feedback.
- Applying a template failure does not clear current create issue draft fields.

## Localization

Add English and Simplified Chinese strings for the new section, even though the confirmed visible Chinese label is `Issue模板`.

Chinese UI terms:

- `Issue模板`
- `新建 Issue模板`
- `选择模板`
- `套用 Issue 模板`
- `来源 · 添加者`
- `更新时间`
- `手动创建`

English terms can use:

- `Issue Templates`
- `New Issue Template`
- `Select template`
- `Apply Issue Template`
- `Source · Creator`
- `Updated`
- `Manually created`

## Testing

Backend tests:

- Create, list, get, update, and delete issue templates.
- Workspace scoping prevents access across workspaces.
- Validation rejects missing template name or issue title.
- Permission behavior matches the chosen Skill-aligned rules.

Frontend tests:

- Sidebar renders `Issue模板` in the configuration group after `Skill`.
- `Issue模板` list renders table columns and template rows.
- Search matches template name, issue title, and issue content.
- Detail page saves edits and deletes with confirmation.
- Create issue modal applies selected template title/content.
- Applying a template with existing title/content shows confirmation before overwrite.
- Cancelling overwrite leaves existing title/content unchanged.
- Applying a template does not change status, priority, assignee, project, due date, parent/child issue state, attachments, or create-another state.
- Empty template selector state points users to the `Issue模板` page.

## Out Of Scope

- Dedicated template issue status.
- Template-specific issue numbering.
- Duplicate or duplicate-to actions.
- Assigning templates to agents.
- Default template status, priority, assignee, project, due date, labels, attachments, or parent/child relationships.
- Importing templates from external sources.
