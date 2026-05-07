# Issue Archive Status Design

## Context

Issue statuses are stored as strings and are surfaced through shared TypeScript
types, issue status configuration, frontend status pickers, and the Multica CLI
status allowlist. The kanban board already uses a dedicated board status list
instead of rendering every issue status.

The requested behavior is to add an `archive` status that users can choose after
an issue is complete, while keeping archived issues out of the board view.

## Product Semantics

`archive` only affects board visibility.

Archived issues remain normal issues for backend list, search, progress, inbox,
and closed/open semantics. In particular, `archive` is not treated as equivalent
to `done` or `cancelled` in server queries or metrics.

## User-Facing Behavior

- Users can set an issue status to `archive` anywhere the normal status picker is
  available.
- Archived issues are hidden from the kanban board because `archive` is not part
  of the board column status list.
- Archived issues can still appear in non-board views such as list/search when
  their data source includes them.
- The CLI accepts `archive` for issue status updates.

## Implementation Scope

Update the shared issue status model and UI configuration:

- Add `archive` to `IssueStatus`.
- Add `archive` to `STATUS_ORDER` and `ALL_STATUSES`.
- Keep `archive` out of `BOARD_STATUSES`.
- Add `archive` styling to `STATUS_CONFIG`.
- Add English and Simplified Chinese status labels.
- Add `archive` to the CLI issue status allowlist.

Leave backend status semantics unchanged:

- Do not add `archive` to `ListOpenIssues` exclusions.
- Do not add `archive` to child issue progress completed counts.
- Do not add `archive` to inbox done/cancelled filters.
- Do not change task cancellation behavior tied to `cancelled`.

## Testing

Add focused tests that prove:

- The shared status list includes `archive`.
- The board status list excludes `archive`.
- CLI issue status validation accepts `archive`.

Existing tests around done/cancelled behavior should continue to pass without
semantic changes.

## Out of Scope

- Database migrations, because issue status is currently a string value rather
  than an enum in the application layer inspected for this change.
- Treating `archive` as a closed status.
- Adding a separate archive browser, restore flow, or bulk archive action.
