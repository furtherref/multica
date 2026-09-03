-- Fork migration (PR #91), renumbered on every upstream sync that brought its
-- own file under the same prefix (342 -> 398 -> 441 -> 450); the migration
-- lint requires unique prefixes and forbids adding new collisions to its
-- historical exception list. The runner keys schema_migrations on the full
-- stem, so a database that already ran an earlier stem replays this file
-- under the new one — hence IF NOT EXISTS, which makes that replay a no-op
-- and leaves the stale ledger row inert.
ALTER TABLE "user"
    ADD COLUMN IF NOT EXISTS account_status TEXT NOT NULL DEFAULT 'active'
    CHECK (account_status IN ('active', 'suspended'));
