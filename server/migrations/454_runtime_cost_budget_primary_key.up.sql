-- Attach the CONCURRENTLY-built unique index as the table's primary key.
--
-- Guarded because 452 is conditionally skippable in practice. A database that
-- ran an EARLIER revision of this branch got 452's table with an inline
-- PRIMARY KEY on id; the current 452 is CREATE TABLE IF NOT EXISTS, so it is
-- recorded without executing, 453's IF NOT EXISTS still builds
-- runtime_cost_budget_pkey_uidx, and an unguarded ALTER here would fail with
-- "multiple primary keys for table are not allowed" — leaving the instance
-- stuck below 454 forever. CLAUDE.md's rule for conditionally skipped
-- migrations: later migrations touching the objects they introduce must be
-- idempotent.
--
-- When the old inline primary key is what exists, the uidx 453 built is a
-- redundant duplicate of the constraint's own index. Nothing depends on it and
-- it is not dropped here: DROP INDEX CONCURRENTLY cannot run in the same
-- multi-statement file, and a plain DROP INDEX would take an ACCESS EXCLUSIVE
-- lock for a purely cosmetic cleanup. Only pre-merge branch databases can be in
-- this state, and an operator who hits it can reclaim the space by hand with
-- `DROP INDEX CONCURRENTLY runtime_cost_budget_pkey_uidx`.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'runtime_cost_budget'::regclass AND contype = 'p'
    ) THEN
        ALTER TABLE runtime_cost_budget
            ADD CONSTRAINT runtime_cost_budget_pkey PRIMARY KEY USING INDEX runtime_cost_budget_pkey_uidx;
    END IF;
END $$;
