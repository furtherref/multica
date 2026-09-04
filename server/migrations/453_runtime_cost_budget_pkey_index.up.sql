-- Backing index for runtime_cost_budget's primary key, attached in 454 via
-- PRIMARY KEY USING INDEX. Own single-statement migration so CONCURRENTLY runs
-- outside an implicit transaction (repo convention).
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS runtime_cost_budget_pkey_uidx
    ON runtime_cost_budget (id);
