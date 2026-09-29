-- Active template names stay unique per workspace; archiving frees the name
-- for reuse without disturbing existing rows (the index only covers active
-- templates). Mirrors idx_issue_status_workspace_name_active.
CREATE UNIQUE INDEX CONCURRENTLY idx_issue_template_workspace_name_active
    ON issue_template (workspace_id, name)
    WHERE archived_at IS NULL;
