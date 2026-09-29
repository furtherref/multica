-- Revert issue template archive support.
ALTER TABLE issue_template DROP COLUMN IF EXISTS archived_at;
