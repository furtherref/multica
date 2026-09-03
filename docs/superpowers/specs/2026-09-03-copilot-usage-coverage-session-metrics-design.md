# Copilot Usage Coverage and Session Metrics Design

## Goal

Stop presenting incomplete Copilot telemetry as complete cost data, and recover
future full token usage from Copilot's local session event files when the CLI
filters usage events from stdout.

This change deliberately does not attempt exact reconciliation with GitHub AI
Credits or Billing API amounts.

## Current failure

Copilot CLI 1.0.80 through 1.0.82 filters `assistant.usage` and
`session.shutdown` from `--output-format json`. Older streams sometimes
retain `assistant.message.outputTokens`, so Multica records output-only rows.
Newer streams often retain no token-bearing stdout event, so a completed task
creates no `task_usage` row.

The runtime page cannot distinguish a verified zero from telemetry that never
arrived. It therefore renders a precise cost for output-only rows and silently
omits dates whose completed tasks have no usage.

The CLI still persists `session.shutdown.data.modelMetrics` in
`~/.copilot/session-state/<session-id>/events.jsonl`. Those cumulative
snapshots contain input, output, cache-read, and cache-write token counts.

## Chosen approach

### 1. Derive usage coverage from existing task data

Add a read-only runtime endpoint that groups completed tasks by the viewer's
timezone and classifies each task from its existing `task_usage` rows:

- `complete`: the task has input-side telemetry
  (`input + cache_read + cache_write > 0`).
- `output_only`: the task has output tokens but no input-side telemetry.
- `missing`: the task completed but has no non-zero token row.

The query uses `agent_task_queue.completed_at` plus a left join to
`task_usage`. It needs no migration and can classify historical gaps that
already exist. Failed and cancelled tasks are excluded because they may have
ended before a provider call; this first version reports coverage only for
completed runs, where missing usage is a genuine telemetry hole.

The endpoint returns daily rows:

```json
{
  "date": "2026-08-29",
  "completed_runs": 261,
  "complete_runs": 0,
  "output_only_runs": 9,
  "missing_runs": 252
}
```

The frontend fetches coverage alongside runtime usage, slices both with the
same selected period and viewing timezone, and renders:

- a warning when the period contains output-only or missing runs;
- a cost lower-bound label and `≥` marker while coverage is incomplete;
- an input/cache "not fully reported" token hint instead of a verified zero;
- a missing-telemetry state even when the usage array itself is empty.

The warning reports counts rather than inventing token or cost values for
missing runs. Existing daily and weekly cost bars remain based only on recorded
usage.

### 2. Recover Copilot usage from the local session file

Before starting a resumed Copilot invocation, read the latest valid
`session.shutdown` snapshot for its session ID as the baseline. After the
process exits and the final session ID is known, read the latest valid snapshot
again:

- fresh session: use the final cumulative snapshot as this run's usage;
- resumed session: subtract the baseline snapshot model by model and token
  bucket by token bucket.

The collector reads only a bounded tail of `events.jsonl`, ignores every
event except `session.shutdown`, and never logs message content. Session IDs
must pass a strict safe-component validation before being used in a path.

Copilot's `inputTokens` includes both cache tiers. The recovered raw snapshot
therefore flows through the existing `addUsage` helper, which stores:

```text
uncached input = inputTokens - cacheReadTokens - cacheWriteTokens
output         = outputTokens
cache read     = cacheReadTokens
cache write    = cacheWriteTokens
```

Usage sources remain mutually exclusive. Resolution order is:

1. complete stdout/session usage already parsed from the stream;
2. local session-file usage (fresh total or safe resume delta);
3. legacy `assistant.message.outputTokens`;
4. no usage.

The session-file source replaces, rather than adds to, overlapping stdout
sources. A missing, truncated, unsafe, or counter-regressed session file never
fails the task: the adapter logs a bounded warning and falls back to the
existing stdout behavior.

## Session-file location

Resolve the home directory from the exact environment passed to the Copilot
subprocess:

- `HOME` on Unix;
- `USERPROFILE` on Windows;
- `os.UserHomeDir()` only as a final fallback.

The file is:

```text
<home>/.copilot/session-state/<validated-session-id>/events.jsonl
```

This matches the observed Linux runtime while keeping tests deterministic via
a task-local `HOME`.

## Custom pricing notice

The current quiet custom-pricing notice is global: any saved override causes
the page to claim that the current period uses custom rates. Adjust it to
distinguish:

- an override that actually prices a model in the selected usage window;
- saved overrides that are not active in the selected window.

Built-in pricing still wins over custom pricing. This change only corrects the
notice; it does not change pricing precedence.

## API compatibility

Add a separate `GET /api/runtimes/:id/usage/coverage` endpoint rather than
changing the existing usage array into an object. Older desktop clients keep
using `/usage` unchanged. New clients parse the coverage response with a
fallback schema so missing or malformed fields degrade to zero counts.

## Error handling and security

- Reject session IDs containing separators, traversal components, or
  characters outside the existing provider ID alphabet.
- Never emit session-file lines, prompt text, tool output, or credentials into
  logs.
- Ignore a partial trailing JSON line and retain the latest earlier valid
  shutdown snapshot.
- Reject negative resume deltas; do not turn a counter reset into fabricated
  positive usage.
- Bound the tail read so one large or corrupted session file cannot create
  unbounded memory use.
- Preserve the current stdout path as a safe fallback.

## Testing

### Go

- Read a fresh-session shutdown snapshot and convert cached input correctly.
- Compute a resumed-session delta without double counting prior turns.
- Merge model keys independently.
- Ignore partial trailing lines.
- Reject unsafe session IDs and negative deltas.
- Prefer complete stdout usage over session-file usage.
- Prefer session-file complete usage over legacy output-only usage.
- Preserve the current no-usage fallback when the file is absent.
- Aggregate complete, output-only, and missing completed tasks by viewing date.

### TypeScript and React

- Parse coverage API drift defensively.
- Keep usage and coverage query keys timezone-scoped.
- Show a lower-bound cost when any selected run is incomplete.
- Render input/cache as incomplete instead of zero.
- Render a missing-only state when completed runs exist but usage rows do not.
- Do not claim saved custom prices are active when the selected rows do not use
  them.

## Rollout and verification

Deploy the daemon and backend together, then run four low-cost checks on the
dedicated Copilot runtime:

1. fresh session without tools;
2. fresh session with tools;
3. resumed session;
4. a second resume of the same session.

For each task, compare the final session snapshot/delta with the stored
`task_usage` row. Confirm new rows have non-zero input/cache, resume turns do
not rebill prior totals, and the daemon's "copilot reported no token usage"
warning stops for successful runs.

Historical dates are classified by the coverage endpoint but are not silently
backfilled by this change. A separate audited backfill remains required if
historical token rows themselves must be reconstructed.

## Rejected alternatives

- Frontend-only inference cannot detect completed tasks that produced no usage
  row, so it cannot explain blank dates.
- Persisting a new coverage table duplicates facts already available from
  `agent_task_queue` and `task_usage` and would add unnecessary migration
  and backfill risk.
- GitHub Billing API ingestion would provide authoritative money but the
  runtime credential lacks enterprise-billing permission, and exact billing is
  outside this change's scope.
