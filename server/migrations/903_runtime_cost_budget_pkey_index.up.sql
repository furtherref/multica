-- Backing index for runtime_cost_budget's primary key, attached in 904 via
-- PRIMARY KEY USING INDEX. Own single-statement migration so CONCURRENTLY runs
-- outside an implicit transaction (repo convention).
--
-- Attaching renames the index to the constraint, so IF NOT EXISTS alone would
-- rebuild a duplicate on a database that already ran the pre-renumber stems.
-- cmd/migrate skips this file when the table already has a primary key.
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS runtime_cost_budget_pkey_uidx
    ON runtime_cost_budget (id);
