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
| Create-time: agent-assigned issue enqueues immediately unless created into `backlog` or `archive` | `server/internal/service/issue.go` (`IssueService.Create` → `maybeEnqueueOnAssign`) | fix wave 3 cited `handler/issue.go:2263-2264`, which pointed at unrelated `validateAssigneePair` code; the gate moved to the service |
| `shouldEnqueueAgentTask` returns false for `backlog` and `archive` (parking lot / retired work) | `server/internal/service/issue.go` (`IssueService.shouldEnqueueAgentTaskWithQueries`, the gate `Create` actually uses); `server/internal/handler/issue.go` (`Handler.shouldEnqueueAgentTask`) mirrors it for the onboarding-shim create path only | line citations dropped: this moved twice in two syncs |
| Backlog → active (not `done`/`cancelled`/`archive`) enqueues on update, via the single shared `WillEnqueueRun` status source | `server/internal/service/issue_trigger.go` (`IssueService.WillEnqueueRun`, status source) | contract text widened to include archive |
| `UpdateIssue` and `BatchUpdateIssues` call the identical `WillEnqueueRun` predicate — there is no separate batch copy of the enqueue rule (MUL-3375) | `server/internal/handler/issue.go` (`UpdateIssue`, `BatchUpdateIssues`) | new citation |
| Child → `done` notifies + wakes the parent, gated by the stage barrier | `server/internal/handler/issue_child_done.go` (`notifyParentOfChildDone`, and its stage barrier gate) | func def `:51` |
| Status change to non-archive statuses (incl. → `cancelled`) does NOT cancel in-flight tasks; only issue deletion does (MUL-4465) | no-cancel notes in `server/internal/handler/issue.go` (`UpdateIssue`, `BatchUpdateIssues`); deletion cancels in `DeleteIssue` / `BatchDeleteIssues` via `CancelTasksForIssue` (`server/internal/service/task.go`) | new citation |
| `archive` (fork status #39) is the one status change that DOES cancel in-flight tasks. Single and batch paths use a pre-write failure gate plus a post-write convergence sweep; an explicit archive retry repeats the post-write sweep. A single-item post-write failure still completes attachment linking and publishes `issue:updated`, then returns 500 with an explicit retry message. Batch items are likewise published and counted as updated; after processing the batch, a 500 response includes `convergence_failed_issue_ids` for targeted retry. | `server/internal/handler/issue.go` (`UpdateIssue` archive gate + convergence sweep; `BatchUpdateIssues` ditto, ending at the `convergence_failed_issue_ids` response), both via `CancelTasksForIssue` | documents the explicit partial-commit recovery contract |
| Restoring from `archive` sweeps straggler tasks BEFORE the status write commits: a sweep failure aborts the single-issue restore outright (issue stays archived), and in the batch loop the issue is skipped without applying its update and without counting toward `updated` | `server/internal/handler/issue.go` (`UpdateIssue` / `BatchUpdateIssues` restore branches), both via `CancelTasksForIssue` | new citation |
| Assigning/promoting into `archive` never enqueues a run | `server/internal/service/issue_trigger.go` (`WillEnqueueRun`, both the assign source and the status source) | re-verified, unchanged |
| Daemon launch checks task status synchronously after `/start` and before provider execution; terminal/deleted tasks skip provider launch, while transient status-read errors fall through to the asynchronous watcher | `server/internal/daemon/daemon.go` (post-`/start` status check; the asynchronous watcher's scope note) | documents the real provider boundary rather than treating the watcher's pre-start read as sufficient |
| `StartTask` / `CompleteTask` do not write issue status (agent CLI owns progress) | `server/internal/service/task.go` (`StartTask` / `CompleteTask` comments) | new citation |
| Runtime brief: status written whenever the work changes it, mid-turn included — starting the issue's own ask → `in_progress` immediately (workflow step 3); delivery → `in_review`, continuing → `in_progress`, stuck → `blocked`; a turn producing none of the issue's own deliverable → no write at any point; the activity kind never decides (research/design/planning/review count as work when they are the ask); no assignee gate; squad leader dispatch is not delivery (MUL-6417) | `server/internal/daemon/execenv/runtime_config_sections.go` (`writeWorkflowIssue`) | new citation |
| Failed task may roll `in_progress` → `todo` when no active task remains | `server/internal/service/task.go` (`HandleFailedTasks`) | new citation |
| Custom statuses inherit their category's behavior in full; enqueue/park contracts resolve the effective category via `issuestatus.Effective` / `Resolve` (MUL-6243). `archive` is NOT a catalog category — no custom status can inherit it — so the archive contracts above stay keyed on the literal status | `server/internal/issuestatus/issuestatus.go` (`Effective`, `Resolve`, `Canonical`) | new citation |
| Runtime brief lists the workspace's active custom statuses grouped by category; catalog rides the claim payload (MUL-6460) | `server/internal/daemon/execenv/runtime_config_sections.go` (`writeIssueStatusCommand`); claim injection in `server/internal/handler/daemon.go` (`buildClaimedTaskResponse`, status catalog block) | new citation |
| Literal-key exceptions to category rules: failed-task rollback writes the `todo` key; merged close-intent PR writes the `done` key | `server/internal/service/task.go` (`HandleFailedTasks`); `server/internal/handler/github.go` (merge close-intent path) | new citation |

Creation with `--status todo` (or any active status — not `backlog`, not
`archive`) on an agent-assigned issue fires the agent immediately; `--status
backlog` parks it with the assignee set but no trigger. Promoting `backlog →
todo` later fires it then via the shared `WillEnqueueRun` status source
(`internal/service/issue_trigger.go`).

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
comment explicitly asks for `multica issue status <parent-id> in_review`. Any
turn may move the status on its own too, judged from what the work changes
about the issue — there is no assignee gate (MUL-6417).

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
