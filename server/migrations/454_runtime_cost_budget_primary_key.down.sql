-- Dropping the constraint also drops the index it was attached to; 453's down
-- migration then finds nothing to drop and is a no-op.
ALTER TABLE runtime_cost_budget DROP CONSTRAINT IF EXISTS runtime_cost_budget_pkey;
