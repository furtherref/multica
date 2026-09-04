-- name: ListRuntimeCostBudgets :many
SELECT * FROM runtime_cost_budget
WHERE runtime_id = $1
ORDER BY user_id NULLS FIRST, created_at;

-- name: UpsertRuntimeCostBudget :one
-- Replaces the three limits of one scope. A changed limit clears the notified
-- markers so a raised or lowered cap notifies again in the current period.
INSERT INTO runtime_cost_budget (
    id, workspace_id, runtime_id, user_id,
    daily_limit_usd_ticks, weekly_limit_usd_ticks, monthly_limit_usd_ticks,
    updated_by
) VALUES (
    $1, $2, $3, sqlc.narg('user_id'),
    sqlc.narg('daily_limit_usd_ticks'), sqlc.narg('weekly_limit_usd_ticks'), sqlc.narg('monthly_limit_usd_ticks'),
    $4
)
ON CONFLICT (runtime_id, user_id) DO UPDATE SET
    daily_limit_usd_ticks   = EXCLUDED.daily_limit_usd_ticks,
    weekly_limit_usd_ticks  = EXCLUDED.weekly_limit_usd_ticks,
    monthly_limit_usd_ticks = EXCLUDED.monthly_limit_usd_ticks,
    daily_notified_period_start   = CASE WHEN runtime_cost_budget.daily_limit_usd_ticks   IS DISTINCT FROM EXCLUDED.daily_limit_usd_ticks   THEN NULL ELSE runtime_cost_budget.daily_notified_period_start   END,
    weekly_notified_period_start  = CASE WHEN runtime_cost_budget.weekly_limit_usd_ticks  IS DISTINCT FROM EXCLUDED.weekly_limit_usd_ticks  THEN NULL ELSE runtime_cost_budget.weekly_notified_period_start  END,
    monthly_notified_period_start = CASE WHEN runtime_cost_budget.monthly_limit_usd_ticks IS DISTINCT FROM EXCLUDED.monthly_limit_usd_ticks THEN NULL ELSE runtime_cost_budget.monthly_notified_period_start END,
    updated_by = EXCLUDED.updated_by,
    updated_at = now()
RETURNING *;

-- name: DeleteRuntimeCostBudgetsExcept :exec
-- PUT is a full replace: every scope not in keep_keys goes away. The
-- runtime-total row (user_id IS NULL) is addressed by the all-zero uuid so
-- one uuid[] can name both kinds of scope.
DELETE FROM runtime_cost_budget
WHERE runtime_id = $1
  AND COALESCE(user_id, '00000000-0000-0000-0000-000000000000'::uuid) <> ALL(@keep_keys::uuid[]);

-- name: DeleteRuntimeCostBudgetsForRuntime :exec
DELETE FROM runtime_cost_budget WHERE runtime_id = $1;

-- name: DeleteRuntimeCostBudgetsForWorkspaceUser :exec
DELETE FROM runtime_cost_budget WHERE workspace_id = $1 AND user_id = $2;

-- name: ListRuntimeSpendByOwner :many
-- Every budget scope and period of one runtime in a single pass. Rows are
-- grouped by the agent's owner and by provider/model, so Go can price the
-- uncosted tokens with the server rate table and then fold the groups into a
-- runtime total and a per-owner total for each period. owner_id is NULL for
-- agents nobody owns: their spend counts toward the runtime total only.
-- Mirrors ListRuntimeUsageByAgent in runtime_usage.sql: cost_usd_ticks is
-- authoritative where present, and the uncosted_* buckets carry the tokens of
-- the rows the provider did not price.
--
-- The three period starts are not ordered against each other -- the current
-- Monday can fall before or after the first of the month -- so the scan floor
-- is their LEAST, and each period picks its own rows with a FILTER.
SELECT
    a.owner_id,
    LOWER(tu.provider) AS provider,
    tu.model,
    COALESCE(SUM(tu.cost_usd_ticks)     FILTER (WHERE tu.created_at >= @daily_start::timestamptz), 0)::bigint AS daily_cost_usd_ticks,
    COALESCE(SUM(tu.input_tokens)       FILTER (WHERE tu.cost_usd_ticks IS NULL AND tu.created_at >= @daily_start::timestamptz), 0)::bigint AS daily_uncosted_input_tokens,
    COALESCE(SUM(tu.output_tokens)      FILTER (WHERE tu.cost_usd_ticks IS NULL AND tu.created_at >= @daily_start::timestamptz), 0)::bigint AS daily_uncosted_output_tokens,
    COALESCE(SUM(tu.cache_read_tokens)  FILTER (WHERE tu.cost_usd_ticks IS NULL AND tu.created_at >= @daily_start::timestamptz), 0)::bigint AS daily_uncosted_cache_read_tokens,
    COALESCE(SUM(tu.cache_write_tokens) FILTER (WHERE tu.cost_usd_ticks IS NULL AND tu.created_at >= @daily_start::timestamptz), 0)::bigint AS daily_uncosted_cache_write_tokens,
    COALESCE(SUM(tu.cost_usd_ticks)     FILTER (WHERE tu.created_at >= @weekly_start::timestamptz), 0)::bigint AS weekly_cost_usd_ticks,
    COALESCE(SUM(tu.input_tokens)       FILTER (WHERE tu.cost_usd_ticks IS NULL AND tu.created_at >= @weekly_start::timestamptz), 0)::bigint AS weekly_uncosted_input_tokens,
    COALESCE(SUM(tu.output_tokens)      FILTER (WHERE tu.cost_usd_ticks IS NULL AND tu.created_at >= @weekly_start::timestamptz), 0)::bigint AS weekly_uncosted_output_tokens,
    COALESCE(SUM(tu.cache_read_tokens)  FILTER (WHERE tu.cost_usd_ticks IS NULL AND tu.created_at >= @weekly_start::timestamptz), 0)::bigint AS weekly_uncosted_cache_read_tokens,
    COALESCE(SUM(tu.cache_write_tokens) FILTER (WHERE tu.cost_usd_ticks IS NULL AND tu.created_at >= @weekly_start::timestamptz), 0)::bigint AS weekly_uncosted_cache_write_tokens,
    COALESCE(SUM(tu.cost_usd_ticks)     FILTER (WHERE tu.created_at >= @monthly_start::timestamptz), 0)::bigint AS monthly_cost_usd_ticks,
    COALESCE(SUM(tu.input_tokens)       FILTER (WHERE tu.cost_usd_ticks IS NULL AND tu.created_at >= @monthly_start::timestamptz), 0)::bigint AS monthly_uncosted_input_tokens,
    COALESCE(SUM(tu.output_tokens)      FILTER (WHERE tu.cost_usd_ticks IS NULL AND tu.created_at >= @monthly_start::timestamptz), 0)::bigint AS monthly_uncosted_output_tokens,
    COALESCE(SUM(tu.cache_read_tokens)  FILTER (WHERE tu.cost_usd_ticks IS NULL AND tu.created_at >= @monthly_start::timestamptz), 0)::bigint AS monthly_uncosted_cache_read_tokens,
    COALESCE(SUM(tu.cache_write_tokens) FILTER (WHERE tu.cost_usd_ticks IS NULL AND tu.created_at >= @monthly_start::timestamptz), 0)::bigint AS monthly_uncosted_cache_write_tokens
FROM task_usage tu
JOIN agent_task_queue atq ON atq.id = tu.task_id
LEFT JOIN agent a ON a.id = atq.agent_id
WHERE atq.runtime_id = $1
  AND tu.created_at >= LEAST(@daily_start::timestamptz, @weekly_start::timestamptz, @monthly_start::timestamptz)
GROUP BY a.owner_id, LOWER(tu.provider), tu.model;

-- name: MarkRuntimeCostBudgetNotified :execrows
-- Claims the "first refusal in this period" notice for one scope and period.
-- Returns 0 rows when the period was already notified, so the caller sends
-- the inbox item only when it wins the claim.
UPDATE runtime_cost_budget SET
    daily_notified_period_start   = CASE WHEN @period::text = 'daily'   THEN @period_start::timestamptz ELSE daily_notified_period_start   END,
    weekly_notified_period_start  = CASE WHEN @period::text = 'weekly'  THEN @period_start::timestamptz ELSE weekly_notified_period_start  END,
    monthly_notified_period_start = CASE WHEN @period::text = 'monthly' THEN @period_start::timestamptz ELSE monthly_notified_period_start END,
    updated_at = now()
WHERE id = @id
  AND (CASE @period::text
         WHEN 'daily'   THEN daily_notified_period_start
         WHEN 'weekly'  THEN weekly_notified_period_start
         ELSE                monthly_notified_period_start
       END) IS DISTINCT FROM @period_start::timestamptz;
