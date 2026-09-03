-- One budget row per (runtime, user) scope. NULLS NOT DISTINCT makes the
-- runtime-total row (user_id IS NULL) unique too, and lets the upsert in
-- runtime_cost_budget.sql target this index with ON CONFLICT.
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_runtime_cost_budget_scope
    ON runtime_cost_budget (runtime_id, user_id) NULLS NOT DISTINCT;
