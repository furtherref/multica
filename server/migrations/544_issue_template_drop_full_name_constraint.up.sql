-- Drop the full (workspace_id, name) unique constraint left by migration 108.
--
-- Migration 543 built the partial unique index
-- idx_issue_template_workspace_name_active on active rows only, so an archived
-- template's name can be reused by a new active template. The legacy full
-- constraint would still reject that reuse across archived rows, so it is
-- dropped here — after the concurrent index is valid, with no gap where a
-- duplicate active name could slip through.
ALTER TABLE issue_template DROP CONSTRAINT issue_template_workspace_id_name_key;
