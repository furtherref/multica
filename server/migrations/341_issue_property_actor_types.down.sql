-- Restore the pre-actor type allowlist. Actor definitions have to go first or
-- the constraint cannot be re-added. Their values stay in each issue's
-- property bag, keyed by a definition id that no longer resolves — the same
-- shape the catalog already tolerates for a removed definition, and readers
-- skip those keys. Re-running the up migration does NOT bring the definitions
-- back, so this is a local-rollback tool, not a reversible production step.
DELETE FROM issue_property WHERE type IN ('actor', 'multi_actor');
ALTER TABLE issue_property DROP CONSTRAINT IF EXISTS issue_property_type_check;
ALTER TABLE issue_property ADD CONSTRAINT issue_property_type_check
    CHECK (type IN ('text', 'number', 'select', 'multi_select', 'date', 'checkbox', 'url'));
