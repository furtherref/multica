# working-on-issues source map

Evidence layer for `SKILL.md`. Every contract the skill states is traced to a
current `file:line` here. Lines were re-derived against `feat/builtin-skills`
after the latest `main` merge; the prior skill cited pre-merge lines that have
since moved (see the "drifted" column). Re-confirm with the verification command
at the bottom before relying on an exact line.

## `multica issue pull-requests` — read PR links from Multica

| Behavior | File:line | Drifted from |
|---|---|---|
| CLI command `pull-requests <id>` (alias `prs`) | `server/cmd/multica/cmd_issue.go:105` | `:104` |
| `runIssuePullRequests` handler | `server/cmd/multica/cmd_issue.go:507` | new citation |
| Calls `GET /api/issues/<id>/pull-requests` | `server/cmd/multica/cmd_issue.go:522` | `:522` (unchanged) |
| API route registration | `server/cmd/server/router.go:480` | `:480` (unchanged) |
| Handler `ListPullRequestsForIssue` → `Queries.ListPullRequestsByIssue` | `server/internal/handler/github.go:466,471` | `:466` (unchanged) |
| Row → response mapper `issuePullRequestRowToResponse` | `server/internal/handler/github.go:149` | new citation |

The CLI resolves the issue ref, GETs the endpoint, and (for `--output json`)
prints the raw `{"pull_requests": [...]}` body. Only `--output` is accepted; the
default `table` shows `NUMBER STATE TITLE URL`.

## PR response shape

`GitHubPullRequestResponse` struct: `server/internal/handler/github.go:51`. JSON
fields the agent can read off each element of `pull_requests`:

- `number` (`json:"number"`, line 56)
- `html_url` (`json:"html_url"`, line 59)
- `title` (`json:"title"`, line 57)
- `state` (`json:"state"`, line 58) — the folded lifecycle enum (see below)
- `merged_at` (`json:"merged_at"`, line 63), `closed_at` (line 64)
- `mergeable_state` (`json:"mergeable_state"`, line 70) — mirrors GitHub; UI only
  surfaces `clean`/`dirty`, other values round-trip as unknown
- `checks_conclusion` (`json:"checks_conclusion"`, line 74) — aggregated
  `"passed"`/`"failed"`/`"pending"` or `null` (no observed suite)
- `checks_passed` / `checks_failed` / `checks_pending` (lines 78-80) — per-suite
  counts; `aggregateChecksConclusion` (line 183) folds them into
  `checks_conclusion`

There is **no** standalone `draft` or `merged` boolean in the response. The
PR lifecycle is encoded in the single `state` string by `derivePRState`
(`server/internal/handler/github.go:994`):

```
merged   → if PullRequest.Merged
closed   → else if PullRequest.State == "closed"
draft    → else if PullRequest.Draft
open     → otherwise
```

`derivePRState` is called when the webhook upserts the row
(`server/internal/handler/github.go:682`), so `state` is what the list endpoint
returns. "Is it merged?" = `state == "merged"` (or `merged_at != null`); "is it a
draft?" = `state == "draft"`. Combine with `checks_conclusion` for CI status.

## Two distinct webhook paths: link vs close-intent

Both run inside the `pull_request` webhook handler, gated by the workspace
auto-link flag (`workspaceAutoLinkPRsEnabled`, `github.go:1074`).

### Path 1 — link (title OR body OR branch)

- `extractIdentifiers` regex helper: `server/internal/handler/github.go:1028`
- driving regex `identifierRe` (`\b([a-z][a-z0-9]{1,9})-(\d+)\b`, case-insensitive):
  `server/internal/handler/github.go:490`
- call site: `server/internal/handler/github.go:727` —
  `extractIdentifiers(p.PullRequest.Title, p.PullRequest.Body, p.PullRequest.Head.Ref)`

Every `PREFIX-NUMBER` mention in **title, body, or branch** resolves to an issue
in the workspace and writes a link row (`LinkIssueToPullRequest`, ~`github.go:762`).
This is what `multica issue pull-requests` later reads back.

**Reference-only flag (MUL-3739).** The link row carries a `reference_only`
boolean (`migrations/127_issue_pull_request_reference_only.up.sql`). The handler
computes a `qualifyingIdents` set = identifiers in **title or branch** (any
`extractIdentifiers` match) ∪ **body closing keywords** (`closingIdents`). A
linked identifier NOT in that set was matched only by a bare body mention, so its
row is written with `reference_only = true`. Both `ListPullRequestsByIssue` and
`GetIssuePullRequestCloseAggregate` filter `AND NOT reference_only`, so
reference-only links are hidden from the CLI / UI PR list **and** excluded from
the auto-advance gate (an open body-only mention must not silently block the
issue from reaching `done` while invisible in the list). The row still exists for
edit-time close-intent tracking. `reference_only` follows the same
`preserve_close_intent` terminal gate as `close_intent`.

Drifted from the prior skill's `github.go:727` citation, which pointed at the old
call-site location for the link logic.

### Path 2 — close intent (title OR body only, keyword-adjacent)

- `extractClosingIdentifiers` regex helper: `server/internal/handler/github.go:1051`
- driving regex `closingIdentifierRe`
  (`\b(?:close[sd]?|fix(?:e[sd])?|resolve[sd]?)[:\s]+([a-z][a-z0-9]{1,9})-(\d+)\b`):
  `server/internal/handler/github.go:501`
- call site: `server/internal/handler/github.go:736` —
  `extractClosingIdentifiers(p.PullRequest.Title, p.PullRequest.Body)` (no branch arg)

Only a `PREFIX-NUMBER` immediately after a closing keyword
(`Closes`/`Fixes`/`Resolves`, optional `:` then whitespace) sets the link row's
`close_intent` flag — the gate that auto-advances the issue to `done` on merge.
`Fix MUL-1` closes; `Fix login MUL-1` does not (adjacency). Branch names are
deliberately excluded (function doc, `github.go:1044-1050`): a branch like
`mul-1/fix-login` links but must never declare close intent.

Drifted from the prior skill's `github.go:736` citation.

Net: a bare title prefix (`MUL-2759: ...`) or a branch ref links only (shown in
the PR list); `Closes MUL-2759` links **and** records close intent; a bare body
mention with no title/branch ref and no closing keyword links as `reference_only`
and is hidden from the PR list.

## Status side effects (enqueue contracts)

| Behavior | File:line | Drifted from |
|---|---|---|
| Create-time: agent-assigned issue enqueues immediately unless created into `backlog` or `archive` | `server/internal/service/issue.go:395` (call site inside `maybeEnqueueOnAssign`, defined at `:391` and called by `IssueService.Create` at `:284`) | fix wave 3: old `:2263-2264` pointed at unrelated `validateAssigneePair` code |
| `shouldEnqueueAgentTask` returns false for `backlog` and `archive` (parking lot / retired work) | `server/internal/service/issue.go:413-415` (the gate `IssueService.Create` actually uses); `server/internal/handler/issue.go:2891-2895` mirrors it for the onboarding-shim create path only (`internal/handler/onboarding_shim.go:328`) | fix wave 3: old `:2644-2648` pointed at unrelated stage-field handling |
| Backlog → active (not `done`/`cancelled`/`archive`) enqueues on update, via the single shared `WillEnqueueRun` status source | `server/internal/service/issue_trigger.go:109-114` | fix wave 3: old `:2537-2540` pointed at unrelated priority-field handling; contract text widened to include archive |
| `UpdateIssue` and `BatchUpdateIssues` call the identical `WillEnqueueRun` predicate — there is no separate batch copy of the enqueue rule (MUL-3375) | `server/internal/handler/issue.go:2775-2785` (`UpdateIssue`) and `:3309-3319` (`BatchUpdateIssues`) | fix wave 3: old `:3021-3024` pointed at unrelated code; the old "same contract" phrasing implied a duplicated copy, which was never accurate — it is the same call |
| Child → `done` notifies + wakes the parent, gated by the stage barrier | `server/internal/handler/issue_child_done.go:68` (`notifyParentOfChildDone`; doc comment at `:16`; barrier gate at `:124`) | fix wave 3: was func def `:66`/comment `:15`/gate `:115` |
| Status change to non-archive statuses (incl. → `cancelled`) does NOT cancel in-flight tasks; only issue deletion does (MUL-4465) | no-cancel note in `server/internal/handler/issue.go:2774-2797` (`UpdateIssue` comprehensive comment block); batch version refers back to UpdateIssue (see `:3325-3331`); deletion still cancels at `:3002` (`DeleteIssue`) / `:3412` (`BatchDeleteIssues`) via `CancelTasksForIssue` (`server/internal/service/task.go:1586`) | fix wave 4: corrected no-cancel note location to the comprehensive comment block (was `:2762`); delete-cancel moved from `:2992`/`:3400` to `:3002`/`:3412` |
| `archive` (fork status #39) is the one status change that DOES cancel in-flight tasks, executing pre-write: cancel precedes the archive commit, so a reported archive means in-flight tasks are already cancelled; cancel failure aborts the single-issue update outright (returns 500), and in batch loop the issue is skipped without applying its update and without counting toward `updated` | `server/internal/handler/issue.go:2699-2714` (`UpdateIssue` pre-write block) and `:3283-3297` (`BatchUpdateIssues` pre-write block), both via `CancelTasksForIssue`; mirrors the restore-sweep pre-write pattern documented in row 126 | fix wave 4: moved pre-write; the old citations `:2787-2798`/`:3324-3332` were the large comment block explaining archive exceptions (now at `:2774-2797` for UpdateIssue) |
| Assigning/promoting into `archive` never enqueues a run | `server/internal/service/issue_trigger.go:105` (assign source) and `:110` (status source), inside `WillEnqueueRun` | re-verified fix wave 3, unchanged |
| Restoring from `archive` sweeps straggler tasks BEFORE the status write commits, not after (fix wave 3): a sweep failure aborts the single-issue restore outright (issue stays archived), and in the batch loop the issue is skipped without applying its update and without counting toward `updated` | `server/internal/handler/issue.go:2683-2690` (`UpdateIssue`, before `h.Queries.UpdateIssue`) and `:3265-3271` (`BatchUpdateIssues`, before that iteration's `h.Queries.UpdateIssue`), both via `CancelTasksForIssue` | fix wave 3: moved pre-write; was post-write at `:2771-2780` / `:3297-3302` |

Creation with `--status todo` (or any active status — not `backlog`, not
`archive`) on an agent-assigned issue fires the agent immediately; `--status
backlog` parks it with the assignee set but no trigger. Promoting `backlog →
todo` later fires it then via the shared `WillEnqueueRun` status source
(`internal/service/issue_trigger.go:109-114`).

Moving an issue to `cancelled` used to call `CancelTasksForIssue` and stop every
active task on it (the old #940 behavior). MUL-4465 removed that from both
`UpdateIssue` and `BatchUpdateIssues`: a status flip to a non-archive status —
`cancelled` included — never cancels tasks now. The fork-original `archive`
status (#39) is the one exception, re-adding a `CancelTasksForIssue` call on
the archive transition (and on the archive → active restore transition, to
sweep stragglers) — see the two rows above. Outside archive,
`CancelTasksForIssue` fires only from the issue-deletion paths (`DeleteIssue` /
`BatchDeleteIssues`), where the owning issue row is going away, so no task is
left orphaned.

## Sub-issue stages (barrier wake)

| Behavior | File:line | Drifted from |
|---|---|---|
| `issue.stage` column (nullable, `>= 1`) | `server/migrations/123_issue_stage.up.sql` | |
| Stage barrier: notify+wake fire only when the lowest unfinished stage is all-terminal; unstaged set = one implicit stage | `server/internal/handler/issue_child_done.go:372` (`stageBarrierClosed`) | fix wave 3: was :231 |
| Per-stage summary + next stage for the wake comment | `server/internal/handler/issue_child_done.go:404` (`stageProgressSummary`) | fix wave 3: was :254 |
| `--stage` on `issue create` / `issue update` | `server/cmd/multica/cmd_issue.go:328,350` | |
| `multica issue children <id>` (sub-issues grouped by stage) | `server/cmd/multica/cmd_issue.go:114,678`; route `GET /api/issues/{id}/children` → `ListChildIssues` | |

Advancement is agent-driven: the server only detects the closed barrier and
wakes the parent assignee. Promoting the next stage's `backlog` sub-issues to
`todo` is the woken agent's decision, not a server side effect.

## Metadata CLI

| Behavior | File:line |
|---|---|
| `multica issue metadata set <issue-id> --key --value [--type]` | `server/cmd/multica/cmd_issue_metadata.go:80,109-111` |
| `multica issue metadata delete <issue-id> --key` | `server/cmd/multica/cmd_issue_metadata.go:93,113` |
| API routes (PUT/DELETE `/metadata/{key}`) | `server/cmd/server/router.go:478-479` |

`--value` is JSON-parsed by default (bool/number sniff); `--type` forces
`string`/`number`/`bool`.

## Custom properties CLI

| Behavior | File:line |
|---|---|
| `multica property list/get/create/update/archive/unarchive` | `server/cmd/multica/cmd_property.go` |
| `multica issue property list/set/unset` (name→id translation) | `server/cmd/multica/cmd_property.go` (`encodeIssuePropertyValue`) |
| Definition CRUD, admin gate, agent-actor rejection | `server/internal/handler/property.go` (`requirePropertyAdmin`) |
| Per-type value validation (self-correcting errors) | `server/internal/handler/property.go` (`validatePropertyValue`) |
| API routes (`/api/properties`, PUT/DELETE `/api/issues/{id}/properties/{propertyId}`) | `server/cmd/server/router.go` |

## Verification command

Re-derive any line above before depending on it:

```bash
cd server
grep -n 'pull-requests <id>'                 cmd/multica/cmd_issue.go
grep -n 'ListPullRequestsForIssue'           cmd/server/router.go internal/handler/github.go
grep -n 'func issuePullRequestRowToResponse\|type GitHubPullRequestResponse struct\|func derivePRState\|func extractIdentifiers\|func extractClosingIdentifiers\|closingIdentifierRe' internal/handler/github.go
grep -n 'extractIdentifiers(\|extractClosingIdentifiers(\|derivePRState(' internal/handler/github.go
grep -n 'qualifyingIdents\|reference_only\|ReferenceOnly' internal/handler/github.go pkg/db/queries/github.sql
grep -n 'in.PrevStatus == "backlog"\|func (s \*IssueService) WillEnqueueRun' internal/service/issue_trigger.go
grep -n 'func (s \*IssueService) shouldEnqueueAgentTask\|func (h \*Handler) shouldEnqueueAgentTask' internal/service/issue.go internal/handler/issue.go
grep -n 'func (h \*Handler) notifyParentOfChildDone'       internal/handler/issue_child_done.go
```
