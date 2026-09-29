-- Issue Template CRUD
--
-- Templates archive instead of delete (RIC-906): archived_at IS NOT NULL
-- retires a template from the default list and the create-issue template
-- picker, while keeping the row for audit and unarchiving. See migration
-- 542/543.

-- name: ListIssueTemplateSummariesByWorkspace :many
-- Default list — active templates only. The create-issue picker and the
-- management list use this, so archived templates are never offered for
-- selection.
SELECT id, workspace_id, name, issue_title, config, created_by, created_at, updated_at
FROM issue_template
WHERE workspace_id = $1
  AND archived_at IS NULL
ORDER BY name ASC;

-- name: ListIssueTemplateSummariesIncludingArchivedByWorkspace :many
-- Admin view: includes archived templates so they can be unarchived.
SELECT id, workspace_id, name, issue_title, config, created_by, created_at, updated_at, archived_at
FROM issue_template
WHERE workspace_id = $1
ORDER BY archived_at IS NULL ASC, name ASC;

-- name: GetIssueTemplateInWorkspace :one
-- Resolves by id AND workspace so a template id from another workspace can
-- never be read or mutated through this handler.
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

-- name: ArchiveIssueTemplate :one
-- Retires a template from future selection only. Issues already created from
-- the template keep their content — templates are only applied at creation.
UPDATE issue_template SET
    archived_at = now(),
    updated_at = now()
WHERE id = $1
  AND workspace_id = $2
  AND archived_at IS NULL
RETURNING *;

-- name: UnarchiveIssueTemplate :one
-- Brings a template back to the active list. The partial unique index on
-- (workspace_id, name) WHERE archived_at IS NULL rejects an unarchive when
-- the freed name was already reused by an active template.
UPDATE issue_template SET
    archived_at = NULL,
    updated_at = now()
WHERE id = $1
  AND workspace_id = $2
  AND archived_at IS NOT NULL
RETURNING *;

-- name: DeleteIssueTemplate :exec
-- Hard delete remains for workspace teardown and the rare true-removal case;
-- normal user flows use ArchiveIssueTemplate.
DELETE FROM issue_template WHERE id = $1;
