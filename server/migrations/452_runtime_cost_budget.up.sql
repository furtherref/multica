-- Per-runtime model cost budgets. One row per scope: user_id IS NULL is the
-- runtime total (blocks every user when reached); a non-NULL user_id caps the
-- tasks whose agent that user owns. A NULL limit means unlimited for that
-- period, and a row whose three limits are all NULL is deleted by the
-- application rather than kept, so "no row" is the only representation of
-- "no budget". Limits are ticks of 1e-10 USD (pricing.CostUSDTicksPerUSD).
-- The *_notified_period_start columns remember which period already got its
-- "limit reached" inbox notice so the notice fires once per period.
-- No foreign keys by repository rule; runtime delete and member revoke remove
-- rows in application code.
CREATE TABLE IF NOT EXISTS runtime_cost_budget (
    id                              UUID PRIMARY KEY,
    workspace_id                    UUID NOT NULL,
    runtime_id                      UUID NOT NULL,
    user_id                         UUID,
    daily_limit_usd_ticks           BIGINT,
    weekly_limit_usd_ticks          BIGINT,
    monthly_limit_usd_ticks         BIGINT,
    daily_notified_period_start     TIMESTAMPTZ,
    weekly_notified_period_start    TIMESTAMPTZ,
    monthly_notified_period_start   TIMESTAMPTZ,
    updated_by                      UUID,
    created_at                      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT runtime_cost_budget_limits_positive CHECK (
        (daily_limit_usd_ticks   IS NULL OR daily_limit_usd_ticks   > 0) AND
        (weekly_limit_usd_ticks  IS NULL OR weekly_limit_usd_ticks  > 0) AND
        (monthly_limit_usd_ticks IS NULL OR monthly_limit_usd_ticks > 0)
    )
);
