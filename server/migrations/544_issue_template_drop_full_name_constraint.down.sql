-- Re-add the full unique constraint on (workspace_id, name) that migration
-- 544 dropped. This runs only on rollback of the archive feature; active
-- names were already unique via the partial index, so a plain constraint add
-- succeeds unless archived rows collide with active ones (in which case the
-- rollback correctly surfaces the conflict).
ALTER TABLE issue_template ADD CONSTRAINT issue_template_workspace_id_name_key UNIQUE (workspace_id, name);
