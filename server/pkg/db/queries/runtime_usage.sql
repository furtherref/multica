-- name: ListRuntimeUsage :many
-- Reads from the UTC-bucketed `task_usage_hourly` rollup table,
-- aggregated to per-(date, provider, model) under the
-- caller-supplied @tz. Powers the trend chart on the runtime detail
-- page and the per-row cost cell on the runtimes list.
--
-- @tz is required, even if the caller intends "UTC", so the bucket
-- cast is unambiguous — `bucket_hour` is UTC and the caller picks the
-- calendar boundary per request.
--
-- provider is LOWER()-normalized so mixed-case historical rows merge
-- (same reason as ListRuntimeUsageByAgent below).
SELECT
    DATE(bucket_hour AT TIME ZONE sqlc.arg('tz')::text) AS date,
    DATE(bucket_hour AT TIME ZONE 'UTC') AS pricing_date,
    LOWER(provider) AS provider,
    model,
    SUM(input_tokens)::bigint        AS input_tokens,
    SUM(output_tokens)::bigint       AS output_tokens,
    SUM(cache_read_tokens)::bigint   AS cache_read_tokens,
    SUM(cache_write_tokens)::bigint  AS cache_write_tokens,
    SUM(cost_usd_ticks)::bigint                                          AS cost_usd_ticks,
    SUM(COALESCE(uncosted_input_tokens, input_tokens))::bigint           AS uncosted_input_tokens,
    SUM(COALESCE(uncosted_output_tokens, output_tokens))::bigint         AS uncosted_output_tokens,
    SUM(COALESCE(uncosted_cache_read_tokens, cache_read_tokens))::bigint AS uncosted_cache_read_tokens,
    SUM(COALESCE(uncosted_cache_write_tokens, cache_write_tokens))::bigint AS uncosted_cache_write_tokens
FROM task_usage_hourly
WHERE runtime_id = $1
  AND bucket_hour >= sqlc.arg('since')::timestamptz
GROUP BY DATE(bucket_hour AT TIME ZONE sqlc.arg('tz')::text), DATE(bucket_hour AT TIME ZONE 'UTC'), LOWER(provider), model
ORDER BY DATE(bucket_hour AT TIME ZONE sqlc.arg('tz')::text) DESC, DATE(bucket_hour AT TIME ZONE 'UTC'), LOWER(provider), model;

-- name: ListRuntimeUsageCoverage :many
-- Classifies completed runs by whether their stored task_usage contains a
-- complete input-side breakdown, output-only telemetry, or no token telemetry.
-- Failed/cancelled runs are excluded because they may have ended before the
-- provider was invoked; a completed run without usage is the billing hole this
-- report is meant to surface. A run the provider priced itself
-- (cost_usd_ticks > 0) is complete regardless of its token buckets: adapters
-- that only report a dollar amount leave every token column at zero.
WITH per_task AS (
    SELECT
        atq.id,
        DATE(atq.completed_at AT TIME ZONE sqlc.arg('tz')::text) AS date,
        COALESCE(SUM(tu.input_tokens), 0)::bigint AS input_tokens,
        COALESCE(SUM(tu.output_tokens), 0)::bigint AS output_tokens,
        COALESCE(SUM(tu.cache_read_tokens), 0)::bigint AS cache_read_tokens,
        COALESCE(SUM(tu.cache_write_tokens), 0)::bigint AS cache_write_tokens,
        COALESCE(SUM(tu.cost_usd_ticks), 0)::bigint AS cost_usd_ticks
    FROM agent_task_queue atq
    LEFT JOIN task_usage tu ON tu.task_id = atq.id
    WHERE atq.runtime_id = sqlc.arg('runtime_id')
      AND atq.status = 'completed'
      AND atq.completed_at >= sqlc.arg('since')::timestamptz
    GROUP BY atq.id, DATE(atq.completed_at AT TIME ZONE sqlc.arg('tz')::text)
)
SELECT
    date,
    COUNT(*)::bigint AS completed_runs,
    COUNT(*) FILTER (
        WHERE input_tokens + cache_read_tokens + cache_write_tokens > 0
           OR cost_usd_ticks > 0
    )::bigint AS complete_runs,
    COUNT(*) FILTER (
        WHERE input_tokens + cache_read_tokens + cache_write_tokens = 0
          AND cost_usd_ticks = 0
          AND output_tokens > 0
    )::bigint AS output_only_runs,
    COUNT(*) FILTER (
        WHERE input_tokens + output_tokens + cache_read_tokens + cache_write_tokens = 0
          AND cost_usd_ticks = 0
    )::bigint AS missing_runs
FROM per_task
GROUP BY date
ORDER BY date DESC;

-- name: GetRuntimeTaskHourlyActivity :many
-- Hour-of-day distribution for queue starts. Bucketed in the viewer's
-- tz so "this runtime is busy in the afternoon" actually means
-- the operator's afternoon, not UTC's.
SELECT EXTRACT(HOUR FROM started_at AT TIME ZONE @tz::text)::int AS hour,
       COUNT(*)::int AS count
FROM agent_task_queue
WHERE runtime_id = $1 AND started_at IS NOT NULL
GROUP BY hour
ORDER BY hour;

-- name: ListRuntimeUsageByAgent :many
-- Per-(agent, provider, model) token aggregates for a runtime since a cutoff. Powers
-- the runtime-detail "Cost by agent" tab. task_usage only carries task_id,
-- so we join the queue to expose agent_id. The model dimension is kept on
-- purpose: cost is computed client-side from a per-model pricing table, so
-- collapsing models server-side would erase the information needed to do
-- that arithmetic. The client groups by agent_id and sums cost per agent.
--
-- This view doesn't bucket by date, so it doesn't need @tz; only the
-- @since cutoff is provided in runtime-local terms (computed in Go).
-- provider is LOWER()-normalized so mixed-case historical rows merge with
-- new rows (see ListDashboardUsageDaily in task_usage.sql).
SELECT
    atq.agent_id,
    DATE(tu.created_at AT TIME ZONE 'UTC') AS pricing_date,
    LOWER(tu.provider) AS provider,
    tu.model,
    SUM(tu.input_tokens)::bigint AS input_tokens,
    SUM(tu.output_tokens)::bigint AS output_tokens,
    SUM(tu.cache_read_tokens)::bigint AS cache_read_tokens,
    SUM(tu.cache_write_tokens)::bigint AS cache_write_tokens,
    COALESCE(SUM(tu.cost_usd_ticks), 0)::bigint AS cost_usd_ticks,
    COALESCE(SUM(tu.input_tokens)       FILTER (WHERE tu.cost_usd_ticks IS NULL), 0)::bigint AS uncosted_input_tokens,
    COALESCE(SUM(tu.output_tokens)      FILTER (WHERE tu.cost_usd_ticks IS NULL), 0)::bigint AS uncosted_output_tokens,
    COALESCE(SUM(tu.cache_read_tokens)  FILTER (WHERE tu.cost_usd_ticks IS NULL), 0)::bigint AS uncosted_cache_read_tokens,
    COALESCE(SUM(tu.cache_write_tokens) FILTER (WHERE tu.cost_usd_ticks IS NULL), 0)::bigint AS uncosted_cache_write_tokens,
    COUNT(DISTINCT tu.task_id)::int AS task_count
FROM task_usage tu
JOIN agent_task_queue atq ON atq.id = tu.task_id
WHERE atq.runtime_id = $1
  AND tu.created_at >= @since::timestamptz
GROUP BY atq.agent_id, DATE(tu.created_at AT TIME ZONE 'UTC'), LOWER(tu.provider), tu.model
ORDER BY atq.agent_id, DATE(tu.created_at AT TIME ZONE 'UTC'), LOWER(tu.provider), tu.model;

-- name: GetRuntimeUsageByHour :many
-- Per-(hour, model) token aggregates (hour ∈ 0..23) for a runtime since a
-- cutoff. Powers the "By hour" tab — shows when in the day this runtime is
-- doing real work, with model preserved for client-side cost calculation
-- (same reason as ListRuntimeUsageByAgent above). Hours with zero activity
-- are omitted; the client fills the 24-bucket axis.
--
-- Hours are extracted in the viewer's tz via @tz so afternoon
-- work bucketed at UTC 06:00 lands in 14:00 for a UTC+8 viewer.
SELECT
    DATE(tu.created_at AT TIME ZONE 'UTC') AS pricing_date,
    EXTRACT(HOUR FROM tu.created_at AT TIME ZONE @tz::text)::int AS hour,
    LOWER(tu.provider) AS provider,
    tu.model,
    SUM(tu.input_tokens)::bigint AS input_tokens,
    SUM(tu.output_tokens)::bigint AS output_tokens,
    SUM(tu.cache_read_tokens)::bigint AS cache_read_tokens,
    SUM(tu.cache_write_tokens)::bigint AS cache_write_tokens,
    COALESCE(SUM(tu.cost_usd_ticks), 0)::bigint AS cost_usd_ticks,
    COALESCE(SUM(tu.input_tokens)       FILTER (WHERE tu.cost_usd_ticks IS NULL), 0)::bigint AS uncosted_input_tokens,
    COALESCE(SUM(tu.output_tokens)      FILTER (WHERE tu.cost_usd_ticks IS NULL), 0)::bigint AS uncosted_output_tokens,
    COALESCE(SUM(tu.cache_read_tokens)  FILTER (WHERE tu.cost_usd_ticks IS NULL), 0)::bigint AS uncosted_cache_read_tokens,
    COALESCE(SUM(tu.cache_write_tokens) FILTER (WHERE tu.cost_usd_ticks IS NULL), 0)::bigint AS uncosted_cache_write_tokens,
    COUNT(DISTINCT tu.task_id)::int AS task_count
FROM task_usage tu
JOIN agent_task_queue atq ON atq.id = tu.task_id
WHERE atq.runtime_id = $1
  AND tu.created_at >= @since::timestamptz
GROUP BY DATE(tu.created_at AT TIME ZONE 'UTC'), EXTRACT(HOUR FROM tu.created_at AT TIME ZONE @tz::text), LOWER(tu.provider), tu.model
ORDER BY hour, DATE(tu.created_at AT TIME ZONE 'UTC'), LOWER(tu.provider), tu.model;
