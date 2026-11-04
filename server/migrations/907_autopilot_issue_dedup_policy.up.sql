-- Per-autopilot issue dedup policy for create_issue mode.
--
-- The recent-duplicate guard from migration 279 only looks `60s` back and is
-- keyed on the interpolated title, so a schedule that fires more often than the
-- title's granularity (e.g. `0 */4 * * *` with a `{{date}}` template) creates one
-- identical-titled issue per firing and burns an agent slot each time (RIC-2291).
--
-- 'per_day' makes the guard collapse every firing in the same local calendar day
-- as the trigger's timezone: the first firing that day creates the issue, later
-- firings that day record a `skipped` run with reason_code
-- 'duplicate_same_day'. The guard applies regardless of whether that day's issue
-- has already reached a terminal status — a merged/deployed day is done, and the
-- merge operation itself must not run twice concurrently.
--
-- 'off' (the default) preserves the existing 60s-title behaviour byte for byte.
ALTER TABLE autopilot
    ADD COLUMN issue_dedup_policy TEXT NOT NULL DEFAULT 'off'
        CHECK (issue_dedup_policy IN ('off', 'per_day'));
