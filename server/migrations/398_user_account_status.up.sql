-- Renumbered from 342 (fork PR #91) after the upstream v0.4.32 sync brought its
-- own 342/343; the migration lint requires unique prefixes and forbids adding
-- new collisions to its historical exception list. The runner keys
-- schema_migrations on the full stem, so a database that already ran
-- 342_user_account_status replays this file under the new stem — hence
-- IF NOT EXISTS, which makes that replay a no-op and leaves the stale 342
-- ledger row inert.
ALTER TABLE "user"
    ADD COLUMN IF NOT EXISTS account_status TEXT NOT NULL DEFAULT 'active'
    CHECK (account_status IN ('active', 'suspended'));
