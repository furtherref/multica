# Issue Archive Status Design

## Context

Issue statuses are stored as strings and are surfaced through shared TypeScript
types, issue status configuration, frontend status pickers, and the Multica CLI
status allowlist. The database also enforces the allowed issue statuses through
the `issue_status_check` constraint. The kanban board already uses a dedicated
board status list instead of rendering every issue status.

The requested behavior is to add an `archive` status that users can choose after
an issue is complete, while keeping archived issues out of the board view.

## Product Semantics

`archive` is a closed issue status.

Archived issues are hidden from active work surfaces, counted as closed in
progress metrics, included wherever the backend currently treats
`done`/`cancelled` issues as finished, and excluded from default open issue and
search results. Moving an issue to `archive` also cancels active agent tasks for
that issue, matching the user-facing terminal behavior of `cancelled`.

## User-Facing Behavior

- Users can set an issue status to `archive` anywhere the normal status picker is
  available.
- Archived issues are hidden from the kanban board because `archive` is not part
  of the board column status list.
- Archived issues are excluded from default open issue and search results unless
  the caller explicitly asks for closed issues or filters for `archive`.
- The CLI accepts `archive` for issue status updates.
- Moving an issue to `archive` cancels queued, dispatched, or running agent
  tasks for that issue.

## Implementation Scope

Update the shared issue status model and UI configuration:

- Add `archive` to `IssueStatus`.
- Add `archive` to `STATUS_ORDER` and `ALL_STATUSES`.
- Keep `archive` out of `BOARD_STATUSES`.
- Add `archive` styling to `STATUS_CONFIG`.
- Add English and Simplified Chinese status labels.
- Add `archive` to the CLI issue status allowlist.
- Add a database migration that updates the `issue.status` check constraint to
  accept `archive`, with a down migration that maps archived issues back to a
  valid non-archive status before restoring the old constraint.
- Add `archive` to backend closed issue sets:
  - `ListOpenIssues` exclusions.
  - Search `includeClosed=false` exclusions.
  - Child issue progress completed counts.
  - Inbox done/cancelled filters.
  - Project linked-issue done counts.
- Cancel active issue tasks when status changes to `archive`, in both single
  issue update and batch issue update paths.

## Testing

Add focused tests that prove:

- The shared status list includes `archive`.
- The board status list excludes `archive`.
- CLI issue status validation accepts `archive`.
- The issue status database constraint accepts `archive` after migrations.
- Backend search/open/progress/inbox SQL treats `archive` as closed.
- Single and batch issue status updates to `archive` cancel active tasks.

Existing tests around done/cancelled behavior should continue to pass with
`archive` added to the same closed-status sets.

## Out of Scope

- Adding a separate archive browser, restore flow, or bulk archive action.

## Addendum (2026-07-16): archive means retired, stricter than done/cancelled

The original semantics above made archive match `cancelled`'s terminal
behavior. The archive-consistency fixes (branch `fix/archive-consistency`)
tighten this: archive now means **retired work whose normal trigger and
execution paths are closed, with active work converged toward cancellation**.
Concretely, and unlike `done`/`cancelled`:

- Creating into, assigning onto, or promoting into `archive` never enqueues.
- Manual rerun returns 409 `issue_archived`; restore first.
- No comment triggers on an archived issue — explicit @mentions are blocked
  with reason `issue_archived`; implicit routing (assignee / thread /
  conversation) is suppressed. `done`/`cancelled` issues stay mentionable.
- Claim, stale-claim recovery, and retry creation exclude tasks whose issue is
  archived. The archive handler cancels active tasks before the status write as
  an early failure gate, then sweeps again after commit to catch tasks that
  became active in between; an explicit archive retry repeats the post-write
  sweep if convergence previously failed.
- After `/start`, the daemon synchronously re-reads task status before invoking
  the provider. A terminal/deleted task skips provider launch; the asynchronous
  watcher remains responsible for cancellation that becomes visible later.
  This is deliberately a best-effort concurrency contract, not an absolute
  zero-spend claim: cancellation can still land after the synchronous read and
  before provider execution, in which case the watcher interrupts it on its
  next poll/reconcile signal.
- Archived issues do not count as active duplicates.
- Archive is terminal for stage barriers and parent wake (single and batch
  paths), and a merged close-intent PR does not resurrect an archived issue.

Restoring from archive re-enables all of the above, cancels any straggler tasks
visible before the restore write, and does not auto-enqueue by itself; runs
start again via the normal assign/promote/mention actions. A known theoretical
residual remains: an insert transaction that reads the issue before archive
and commits only after the restore sweep can strand a runnable task once the
issue becomes active again. Closing that cross-cycle window would need
per-issue generation counters, which this design deliberately omits.
