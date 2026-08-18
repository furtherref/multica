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
| Handler `ListPullRequestsForIssue` → `Queries.ListPullRequestsByIssue` | `server/internal/handler/github.go:687,692` | `:466` |
| Row → response mapper `issuePullRequestRowToResponse` | `server/internal/handler/github.go:205` | `:149` |

The CLI resolves the issue ref, GETs the endpoint, and (for `--output json`)
prints the raw `{"pull_requests": [...]}` body. Only `--output` is accepted; the
default `table` shows `NUMBER STATE TITLE URL`.

## PR response shape

`GitHubPullRequestResponse` struct: `server/internal/handler/github.go:58`. JSON
fields the agent can read off each element of `pull_requests`:

- `provider` (`json:"provider"`, line 63)
- `number` (`json:"number"`, line 67)
- `html_url` (`json:"html_url"`, line 70)
- `title` (`json:"title"`, line 68)
- `state` (`json:"state"`, line 69) — the folded lifecycle enum (see below)
- `merged_at` (`json:"merged_at"`, line 74), `closed_at` (line 75)
- `mergeable_state` (`json:"mergeable_state"`, line 80) — mirrors GitHub; UI only
  surfaces `clean`/`dirty`, other values round-trip as unknown
- `snapshot_available` (`json:"snapshot_available"`, line 100) — for GitHub,
  true only when the App snapshot feature is enabled and the snapshot head
  matches the current PR head (`currentGitHubSnapshotAvailable`, lines 258-265)
- `mergeable` / `merge_state_status` (lines 90, 94) — conflict-only verdict vs
  the complete merge gate; "ready" requires `merge_state_status == "clean"`
- `checks_rollup` (`json:"checks_rollup"`, line 105) and run-level
  `checks_total` / `checks_passed` / `checks_failed` / `checks_running`
  (lines 111-114), plus `failed_check_names` (line 118)
- `checks_conclusion` (`json:"checks_conclusion"`, line 108) — coarse
  `"passed"`/`"failed"`/`"pending"` or `null`; GitHub derives it only from an
  available current-head snapshot (mapper lines 242-254), while self-hosted VCS
  providers use `aggregateChecksConclusion` (line 275)

There is **no** standalone `draft` or `merged` boolean in the response. The
PR lifecycle is encoded in the single `state` string by `derivePRState`
(`server/internal/handler/github.go:1317`):

```
merged   → if PullRequest.Merged
closed   → else if PullRequest.State == "closed"
draft    → else if PullRequest.Draft
open     → otherwise
```

`derivePRState` is called when the webhook upserts the row
(`server/internal/handler/github.go:1115`), so `state` is what the list endpoint
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
| Create-time: agent-assigned issue enqueues immediately unless created into `backlog` or `archive` | `server/internal/service/issue.go:395` (call site inside `maybeEnqueueOnAssign`, defined at `:391` and called by `IssueService.Create` at `:284`) | fix wave 3: old `:2263-2264` pointed at unrelated `validateAssigneePair` code; re-verified fix wave 4 final sweep, unchanged |
| `shouldEnqueueAgentTask` returns false for `backlog` and `archive` (parking lot / retired work) | `server/internal/service/issue.go:413-415` (the gate `IssueService.Create` actually uses); `server/internal/handler/issue.go:2922-2926` mirrors it for the onboarding-shim create path only (`internal/handler/onboarding_shim.go:328`) | fix wave 4 follow-up: handler lines moved after the archive convergence error contract was finalized |
| Backlog → active (not `done`/`cancelled`/`archive`) enqueues on update, via the single shared `WillEnqueueRun` status source | `server/internal/service/issue_trigger.go:109-114` | fix wave 3: old `:2537-2540` pointed at unrelated priority-field handling; contract text widened to include archive; re-verified fix wave 4 final sweep, unchanged |
| `UpdateIssue` and `BatchUpdateIssues` call the identical `WillEnqueueRun` predicate — there is no separate batch copy of the enqueue rule (MUL-3375) | `server/internal/handler/issue.go:2812-2821` (`UpdateIssue`) and `:3370-3379` (`BatchUpdateIssues`) | fix wave 4 follow-up: lines moved after the archive convergence blocks were added |
| Child → `done` notifies + wakes the parent, gated by the stage barrier | `server/internal/handler/issue_child_done.go:68` (`notifyParentOfChildDone`; doc comment at `:16`; barrier gate at `:124`) | fix wave 3: was func def `:66`/comment `:15`/gate `:115`; re-verified fix wave 4 final sweep, unchanged |
| Status change to non-archive statuses (incl. → `cancelled`) does NOT cancel in-flight tasks; only issue deletion does (MUL-4465) | no-cancel note in `server/internal/handler/issue.go:2794-2810`; batch version refers back at `:3360-3365`; deletion cancels at `:3023` / `:3459` via `CancelTasksForIssue` (`server/internal/service/task.go:1586`) | fix wave 4 follow-up: refreshed after adding post-write archive convergence |
| `archive` (fork status #39) is the one status change that DOES cancel in-flight tasks. Single and batch paths use a pre-write failure gate plus a post-write convergence sweep; an explicit archive retry repeats the post-write sweep. A single-item post-write failure still completes attachment linking and publishes `issue:updated`, then returns 500 with an explicit retry message. Batch items are likewise published and counted as updated; after processing the batch, a 500 response includes `convergence_failed_issue_ids` for targeted retry. | single path `server/internal/handler/issue.go:2698-2736,2767-2787,2832-2838`; batch path `:3305-3340,3352-3358,3395-3415`, both via `CancelTasksForIssue` | fix wave 4 follow-up: replaces the inaccurate pre-write-only guarantee and documents the explicit partial-commit recovery contract |
| Daemon launch checks task status synchronously after `/start` and before provider execution; terminal/deleted tasks skip provider launch, while transient status-read errors fall through to the asynchronous watcher | `server/internal/daemon/daemon.go:4031-4054`; watcher scope clarification at `:3038-3042` | fix wave 4 follow-up: documents the real provider boundary rather than treating the watcher's pre-start read as sufficient |
| Assigning/promoting into `archive` never enqueues a run | `server/internal/service/issue_trigger.go:105` (assign source) and `:110` (status source), inside `WillEnqueueRun` | re-verified fix wave 3, unchanged; re-verified fix wave 4 final sweep, unchanged |
| Restoring from `archive` sweeps straggler tasks BEFORE the status write commits, not after: a sweep failure aborts the single-issue restore outright (issue stays archived), and in the batch loop the issue is skipped without applying its update and without counting toward `updated` | `server/internal/handler/issue.go:2674-2696` and `:3289-3303`, both via `CancelTasksForIssue` | fix wave 4 follow-up: refreshed the batch citation after post-write archive convergence was inserted |
| `StartTask` / `CompleteTask` do not write issue status (agent CLI owns progress) | `server/internal/service/task.go` (`StartTask` `:2502` / `CompleteTask` `:2606`) | upstream sync: new citation |
| Assignment brief: ordinary agent `in_progress` then `in_review`; squad leader `in_progress` only on first dispatch | `server/internal/daemon/execenv/runtime_config_sections.go:472` (`writeWorkflowAssignment`) | upstream sync: new citation |
| Failed task may roll `in_progress` → `todo` when no active task remains | `server/internal/service/task.go:3611` (`HandleFailedTasks`) | upstream sync: new citation |

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
both sides of the archive status write (and on the archive → active restore
transition, to sweep stragglers) — see the rows above. Outside archive,
`CancelTasksForIssue` fires only from the issue-deletion paths (`DeleteIssue` /
`BatchDeleteIssues`), where the owning issue row is going away, so no task is
left orphaned.

## Ownership-only assignment and duplicate-run awareness

| Behavior | Source |
|---|---|
| `issue assign --no-start`, `issue update --no-start`, and `issue status --no-start` send `suppress_run=true` | `server/cmd/multica/cmd_issue.go` (`runIssueAssign`, `runIssueUpdate`, `runIssueStatus`) |
| Update and batch-update apply ownership while skipping dispatch when `suppress_run` is true | `server/internal/handler/issue.go` (`UpdateIssue`, `BatchUpdateIssues`) |
| Trusted direct self-assignment suppresses enqueue only when the target `(issue, agent)` already has a non-terminal task | `server/internal/service/issue_trigger.go` (`WillEnqueueRun`), `server/internal/handler/issue_trigger.go` (`shouldSuppressActiveSelfAssignment`) |
| Claim responses expose a bounded, workspace-scoped snapshot of the same agent's other dispatched/running/waiting issue tasks; queued tasks are excluded | `server/pkg/db/queries/agent.sql` (`ListActiveSiblingIssueTasks`), `server/internal/handler/daemon.go` (`buildClaimedTaskResponse`) |
| Daemon prompts point to the target's comment history and concrete sibling `run-messages` commands | `server/internal/daemon/prompt.go` (`buildActiveSiblingRunsBlock`) |

The self-assignment guard is intentionally pair-scoped. It does not treat
"this agent is busy on some other issue" as a reason to suppress a fresh
cross-issue handoff, because serial sub-issue promotion and triage batches rely
on those assignments creating their normal queued runs.

## Sub-issue stages (barrier wake)

| Behavior | File:line | Drifted from |
|---|---|---|
| `issue.stage` column (nullable, `>= 1`) | `server/migrations/123_issue_stage.up.sql` | |
| Stage barrier: notify+wake fire only when the lowest unfinished stage is all-terminal; unstaged set = one implicit stage | `server/internal/handler/issue_child_done.go:403` (`stageBarrierClosed`) | was :372 |
| Per-stage summary + next stage for the wake comment | `server/internal/handler/issue_child_done.go:435` (`stageProgressSummary`) | was :404 |
| `--stage` on `issue create` / `issue update` | `server/cmd/multica/cmd_issue.go:328,350` | |
| `multica issue children <id>` (sub-issues grouped by stage) | `server/cmd/multica/cmd_issue.go:114,678`; stage `done` counting via `isTerminalChildStatus` (resolves custom statuses through `issuestatus.Effective`, MUL-6243); route `GET /api/issues/{id}/children` → `ListChildIssues` | |

Advancement is agent-driven: the server only detects the closed barrier and
wakes the parent assignee. Promoting the next stage's `backlog` sub-issues to
`todo` is the woken agent's decision, not a server side effect. When the woken
assignee (often a squad leader) decides the parent is complete, the system
comment explicitly asks for `multica issue status <parent-id> in_review` —
comment-triggered runs otherwise must not change status unless asked.

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
| Optional catalog icon field and allowlist validation | `server/internal/handler/property.go` (`PropertyResponse`, `validatePropertyIcon`) |
| Per-type value validation (self-correcting errors) | `server/internal/handler/property.go` (`validatePropertyValue`) |
| `actor` / `multi_actor` reference parsing, `member` as the only kind, 20-value cap | `server/internal/handler/property.go` (`actorPropertyKinds`, `parseActorRef`, `parseActorRefList`, `maxPropertyActorValues`) |
| Actor references are checked for workspace membership only | `server/internal/handler/property.go` (`resolveActorRefs`) |
| `--value` name / email / id → `member:<uuid>` resolution (same member lookup as `--assignee`) | `server/cmd/multica/cmd_property.go` (`resolveActorPropertyRef`, `memberOnlyKinds`) |
| Shared actor-reference types and helpers | `packages/core/types/property.ts` (`parseActorRef`, `actorRefsFromValue`, `MAX_ISSUE_PROPERTY_ACTOR_VALUES`) |
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
