-- Issue Template CRUD

-- name: ListIssueTemplateSummariesByWorkspace :many
SELECT id, workspace_id, name, issue_title, config, created_by, created_at, updated_at
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
