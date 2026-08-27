# Network Egress Optimization - Analysis and Design

Date: 2026-08-27
Status: Ready for implementation (2026-08-27).
Revision 1: compression quick wins moved ahead of contract work;
Proposal B rewritten to require visibility filtering instead of a globally
broadcast task-projection event; Agent summary changed from a sub-route to an
additive `projection=summary` parameter.
Revision 2 (after second review): the broadcast leak reclassified as a
current-state security defect with its own early fix stage; per-actor
sequencing dropped in favor of ordered in-connection delivery plus reconnect
resync (multi-node/Redis-relay compatibility); `include_archived: false`
demoted from quick win to Phase 1 (archive-scope counts and Dashboard
deleted-spend bucketing depend on the archived-inclusive list); ETags
restricted to sanitized projections and legacy sensitive routes given
`no-store`; sub-route collision rationale corrected (`loadAgentForUser` is
UUID-only) and the `projection` parameter's silent-downgrade semantics
documented; compression savings restated as a validation target.
Revision 3 (after third review): the realtime security work starts immediately
and runs in parallel with the traffic baseline; the delivery filter now has a
typed routing boundary and an I/O-free Hub hot path; the Agent summary contract
defines `archive_state`, its page envelope, and old-backend fallback; access
logs retain path redaction; and phase references now match the delivery order.
Revision 3a: containment stripping narrowed to `agent_id` only — mobile issue
realtime self-gates on `payload.issue_id`, so `issue_id` must stay in the
broadcast; ETag work items added to Phases 1 and 2 to match Proposal F and
Phase 5.
Revision 4 (after fourth review): containment changed from a blocklist to a
per-event outbound allowlist — `task:dispatch` seeds its payload from the
whole persisted `task.Context` (Quick Create `prompt`, `requester_id`,
`attachment_ids`), which a blocklist cannot contain; ETags switched to weak
validators (`W/"..."`, matching the daemon-workspace precedent) with
`Vary: Accept-Encoding` because compression ships first and RFC 9110 forbids
one strong ETag across content-codings; the stale "atomic replacement"
wording in Security Requirements unified to the close-connection
invalidation model; `task:progress` documented as carrying no `issue_id`
(pre-existing mobile gate limitation) and the TypeScript event contracts
added to the test matrix.
Revision 5 (implementation-ready review): Phase 0A now covers content-bearing
`task:*` and creator-owned `chat:*` events, not only lifecycle metadata;
authorization routing is explicit for Agent-visible lifecycle events,
creator-only direct Chat events, and task-scoped transcript events; the
`task:waiting_local_directory` contract and `wait_reason` are included; and
unknown task or Chat event types fail closed at the external WebSocket
boundary with metrics and structured diagnostics.
Scope: Multica API, web client, desktop client, mobile client, realtime
synchronization, and production observability

## Executive Summary

Production network analysis identified five Multica API routes responsible for
approximately 1,999 MiB of response traffic in the observed sample. The top
three routes account for approximately 95.1% of that traffic:

| Route | Response traffic | Requests | Average response | Share of measured traffic |
|---|---:|---:|---:|---:|
| `/api/agents` | 1,034 MiB | 782 | 1,354 KiB | 51.7% |
| `/api/agent-task-snapshot` | 472 MiB | 1,700 | 284 KiB | 23.6% |
| `/api/issues` | 395 MiB | 2,991 | 135 KiB | 19.8% |
| `/api/tasks/{taskId}/messages` | 58 MiB | 34 | 1,747 KiB | 2.9% |
| `/api/inbox` | 40 MiB | 71 | 577 KiB | 2.0% |

The dominant failure mode is not one slow query or one abnormal user. It is a
fan-out pattern in which large workspace-scoped responses are mounted broadly,
then invalidated and downloaded again after realtime lifecycle events or a
WebSocket reconnect.

The recommended solution is to:

1. immediately contain and close the existing realtime broadcast leaks:
   lifecycle events are delivered only to connections whose actors can see the
   Agent, creator-owned direct Chat events are delivered only to that creator,
   and transcript events are delivered only to authorized task subscribers;
   this work does not wait for the traffic baseline or presence redesign;
2. establish response-byte observability, then verify and enable response
   compression and wire the existing transcript `since` capability into the
   client as bounded early egress improvements;
3. introduce lightweight list and presence projections while preserving the
   existing endpoints for installed clients;
4. stop loading archived and detail-only Agent data from every workspace
   surface, after archive-dependent consumers get their own on-demand
   queries;
5. reduce realtime-driven refetches with visibility-safe cache updates and
   reserve full synchronization for initial load and reconnect;
6. make task transcripts incremental and lazy;
7. move Issue and Inbox projection, grouping, and pagination to the server;
   and
8. use private conditional requests (ETags) on sanitized projections as a
   transitional measure against redundant revalidation while the
   refetch-reduction phases land.

Ordering rationale: the authorization leak is independent of egress cost and
starts remediation immediately. Measurement runs in parallel so it does not
delay containment. After the baseline captures current ingress behavior,
compression and transcript deltas provide bounded early reductions. Agent
prefetch narrowing is not a configuration-only quick win; it ships only after
archive-dependent consumers have dedicated contracts. Projection and refetch
work then reduce uncompressed bytes, database and serialization cost, client
parse cost, and latency.

## Evidence and Verification Boundary

This design is based on:

- the production traffic sample shown above;
- a static review of repository commit `c59d39856`;
- the current API handlers, SQL queries, React Query definitions, and realtime
  cache invalidation code; and
- existing compatibility comments for installed desktop clients.

The duration represented by the production sample was not independently
verified. The production deployment commit, ingress compression behavior, and
actual wire-level response size have also not been verified. Before rollout,
the deployed commit and ingress `Content-Encoding` behavior must be captured.

All percentage and average calculations in this document refer only to the
provided sample. Reduction targets are acceptance goals, not claims about
results already achieved.

## Current-State Root Causes

### 1. Agent List: Detail Payload on a Global Query

`packages/core/workspace/queries.ts` calls `listAgents` with
`include_archived: true`. `packages/core/agents/use-workspace-presence-prefetch.ts`
mounts this query at the workspace layout level so that presence surfaces and
mention suggestions are warm throughout the application.

The server-side list path compounds the payload size:

- `server/pkg/db/queries/agent.sql` uses `SELECT *` for both active-only and
  archived-inclusive list queries.
- `server/internal/handler/agent.go` returns the full `AgentResponse`, including
  instructions, system instructions, runtime configuration, custom arguments,
  MCP configuration, permission targets, skill summaries, disabled runtime
  skills, and archive metadata.
- The list handler loads all Agent skill bindings in the workspace and all
  invocation targets before encoding the response.
- `packages/core/realtime/use-realtime-sync.ts` invalidates the full Agent list
  on generic Agent events, label events, and WebSocket reconnect.

The result is a detail-grade, archive-inclusive response on application
surfaces that usually need only identity and status fields.

### 2. Agent Task Snapshot: Wide Rows Multiplied by Lifecycle Events

`/api/agent-task-snapshot` returns:

- every active Agent task in the workspace; and
- the latest completed or failed task for every Agent.

The latest terminal task exists primarily for the Squad hover card's last
activity line. The code already identifies moving it to a lazy endpoint as a
follow-up requirement.

The snapshot reuses the wide `AgentTaskResponse`. Terminal rows may include
large `result` and `error` values in addition to branch, directory, attribution,
and lifecycle fields. The SQL query was previously improved with an indexed
per-Agent terminal lookup, but that optimization reduces database work only;
it does not change the number of rows, the wire contract, or the frontend
refetch behavior.

Any generic task lifecycle event invalidates the workspace-wide snapshot. A
single run commonly emits queued, dispatched, running, and terminal events.
With multiple clients online, the network cost is approximately:

```text
task lifecycle events x active workspace clients x full snapshot bytes
```

The current 100 ms invalidation debounce only coalesces events that happen in a
very small interval. It does not prevent refetches across normal task state
transitions.

Task message fan-out has already been narrowed so `task:message` does not enter
the generic task invalidation path. That improvement should be retained, but it
does not address lifecycle-driven snapshot refetches.

### 3. Issue Lists: Fixed Request Fan-Out with a Heavy Projection

`packages/core/issues/queries.ts` requests the first page for all seven status
categories in parallel, with up to 50 Issues per category. Fixed category
fan-out prevents custom statuses from increasing the request count further,
but a cold board or list query still generates seven `/api/issues` requests.

The list handler selects and serializes detail-oriented fields such as full
description, metadata, and properties. Labels are also attached for list
rendering, and the shared response type can include reactions and attachments.

The request count is therefore partly structural, while the response size is
driven by a projection that is wider than most cards and list rows require.
Combining the seven requests without narrowing the projection would reduce
request overhead but would not materially reduce response bytes.

### 4. Task Messages: Incremental Server Capability Is Not Used

The server endpoint supports `?since=<seq>` and has a dedicated
`ListTaskMessagesSince` query. The frontend API client always calls the route
without `since`, so every reconciliation returns the complete persisted
transcript.

Completed Assistant messages also mount the task-message query automatically
when a valid task ID is present. This means historical messages can download
their complete tool and reasoning timeline even when the user has not expanded
the transcript. The transcript dialog performs another full reconciliation on
open and when a task becomes terminal.

At approximately 1.7 MiB per request in the sample, this route has low request
volume but a high per-interaction cost.

### 5. Inbox: Server Returns History That the Client Discards

The active Inbox SQL query is unbounded. It returns all active rows for the
recipient, while the frontend later groups those rows by `issue_id` and keeps
only the newest item for each Issue.

Inbox events invalidate both active and archived lists even when the event
contains enough data to update one cache entry. The code currently assumes the
Inbox list is small, which is inconsistent with the measured average response
of approximately 577 KiB.

### 6. Existing Realtime Broadcast Leaks (Current-State Security Defects)

These are not only risks for the future design; they exist today, and they are
worse than activity metadata alone:

- `taskEvent` in `server/internal/service/task.go` builds lifecycle payloads
  containing `task_id`, `agent_id`, `issue_id`, and `status`.
- `broadcastTaskDispatch` in the same file goes further: it unmarshals the
  entire persisted `task.Context` into the outbound payload map before adding
  the identifier fields. For a Quick Create task, `QuickCreateContext`
  includes the user's full `prompt`, `requester_id`, `attachment_ids`,
  priority, and project/squad linkage — all broadcast on `task:dispatch`.
- The `SubscribeAll` listener in `server/cmd/server/listeners.go` fans both
  out to every connection in the workspace with no Agent-visibility
  filtering.
- `task:message` uses the same workspace fanout and carries persisted transcript
  fields including `content`, tool `input`, and tool `output`. The frontend's
  cache guard prevents unopened transcripts from accumulating locally, but the
  frames have already crossed the network and reached the unauthorized client.
- `chat:message` carries the full user message and `chat:done` carries the full
  Assistant response and optional quick actions. These events are also handled
  by `SubscribeAll`, even though the HTTP Chat API requires the current user to
  be the session creator and separately rechecks private-Agent visibility.

A member who cannot see a private Agent through `/api/agent-task-snapshot`
(which filters per actor via `accessibleAgentIDs`) still receives that
Agent's task activity metadata — and on dispatch, task content — over the
WebSocket. The existing PR #5018 / MUL-4159 mitigation only prevents clients
from *writing caches* from these payloads; the payloads themselves still
reach unauthorized connections.

Independently, workspace membership does not authorize a member to read another
member's creator-owned direct Chat. The current WebSocket path nevertheless
sends that Chat's user and Assistant content to every workspace connection.
This is an authorization failure even when both members can see the same
Agent. `task:message` has the same problem for transcript content unless the
connection is authorized for and actively subscribed to the linked task.

The content expansion also shows why a blocklist
(`internalOnlyPayloadKeys`) cannot be the containment mechanism: the leak is
whatever the producer happens to put in the map, so the outbound boundary
must be an explicit per-event allowlist.

Fixing these broadcast paths is therefore an independent security work item
scheduled immediately in Phase 0A below. It is not gated by the egress
measurement or presence redesign.

### 7. Transport and Observability Gaps

The application router does not install a general response-compression
middleware. JSON responses are buffered and assigned an uncompressed
`Content-Length` by `writeJSON`.

Production ingress may still apply gzip or Brotli, so application code alone
does not prove that wire traffic is uncompressed. Both upstream response bytes
and actual ingress bytes must be measured.

The current HTTP metrics record request count and duration by normalized route.
Response-size metrics are limited to the daemon workspace endpoint. The request
logger records method, a path passed through `redactWebhookPath`, status,
duration, request ID, and available client metadata, but it does not record
response bytes or workspace identity.

## Goals

1. Reduce response traffic for the five identified routes without weakening
   authorization or exposing workspace data across users.
2. Preserve compatibility with installed desktop clients during migration.
3. Keep presence and Agent state responsive under normal WebSocket operation.
4. Recover deterministically after reconnects or missed realtime events.
5. Bound every growing list or transcript response.
6. Make future regressions visible through route-level request and byte metrics.

## Non-Goals

- Rewriting the realtime transport protocol.
- Removing persisted task transcript data or reducing its audit value.
- Changing Agent visibility or invocation authorization semantics.
- Introducing shared CDN caching for authenticated workspace resources.
- Treating database latency improvements as a substitute for reducing response
  size and request frequency.
- Removing legacy endpoints before supported installed clients have migrated.

## Proposed Design

### A. Lightweight Agent Summary Contract

Add a summary projection to the existing list route as an additive query
parameter:

```http
GET /api/agents?projection=summary&archive_state=active&cursor=<opaque>&limit=<n>&include_archived=true
```

A parameter is preferred over a `GET /api/agents/summary` sub-route because
it is purely additive: old clients keep their exact current behavior, and no
new route registration is needed. (Route collision is not a concern either
way — `loadAgentForUser` accepts only UUIDs via `parseUUIDOrBadRequest`, so a
literal `summary` segment could never shadow an Agent.) Omitting `projection`
returns the legacy full response unchanged.

For `projection=summary`, `archive_state` is required to have one of these
values:

- `active`: active Agents only; this is the global prefetch default;
- `archived`: archived Agents only; the archive view requests this on demand;
  or
- `all`: active and archived Agents, reserved for consumers that explicitly
  need both sets.

The legacy `include_archived` parameter keeps its current behavior when
`projection` is omitted. When `projection=summary`, the new server ignores it
and uses `archive_state` exclusively. New clients nevertheless send
`include_archived=true` during the compatibility window so an older server
that silently ignores the new parameters returns the complete legacy set for
local scope filtering and count derivation. It must not define summary scope:
`include_archived=true` means active plus archived, not archived only.

The summary response is a page envelope, not a bare array:

```json
{
  "items": [
    {
      "id": "agent-id",
      "workspace_id": "workspace-id",
      "runtime_id": "runtime-id",
      "runtime_bound": true,
      "name": "Engineer",
      "description_preview": "Short bounded description",
      "avatar_url": null,
      "status": "active",
      "owner_id": "user-id",
      "permission_mode": "private",
      "archived_at": null,
      "updated_at": "2026-08-27T00:00:00Z"
    }
  ],
  "has_more": true,
  "next_cursor": "opaque-cursor",
  "counts": {
    "active": 12,
    "archived": 3
  }
}
```

`items` is ordered by `(created_at, id)` with an opaque cursor over the same
tuple. `limit` has a server-side maximum. `next_cursor` is null when
`has_more=false`. `counts` is actor-visibility-filtered and is returned on the
cursorless first page so the Agents page can render active and archive scope
counts without downloading archived rows; later pages may omit it.

The parameter's downgrade path is explicit. An older backend ignores
`projection`, `archive_state`, `cursor`, and `limit`, honors the compatibility
`include_archived=true`, then returns the legacy `Agent[]`. The new client
decoder accepts either `AgentSummaryPage` or the legacy array on the
cursorless first request. For a legacy array, it filters and projects the rows
locally, computes counts from that complete response, marks `has_more=false`,
and emits an `old_backend_full_agents` telemetry cause. It does not send a
cursor request in legacy mode. Receiving a legacy array for a non-empty cursor
is a protocol error rather than a silent pagination reset. The decoder must
discriminate the raw array-versus-object shape before schema parsing; a body
matching neither contract is a protocol error, not an empty fallback.

Each summary item contains only fields required by list rows, mentions,
presence surfaces, and lightweight permission filtering.

The endpoint must not include:

- instructions or system instructions;
- runtime configuration or custom arguments;
- MCP configuration;
- skill descriptions or disabled-skill configuration;
- invocation target details; or
- any environment-variable values.

The new web and desktop clients should use `archive_state=active` for global
workspace prefetch. Archived Agents are loaded only when the archive view is
opened, using `archive_state=archived` and its own query key. Dashboard and
other historical attribution surfaces that need archived identities must use
an explicit archive-aware summary query instead of the active global cache.

`GET /api/agents/{id}` remains the detail contract. Dedicated detail queries
should load skills, invocation targets, and other configuration only on
surfaces that render or edit them.

The default (projection-less) `/api/agents` response remains unchanged during
the compatibility window.

### B. Presence Snapshot and Realtime Cache Reduction

Add a lightweight endpoint:

```http
GET /api/agent-presence
```

It should return only active task records required to derive Agent presence:

```json
{
  "tasks": [
    {
      "task_id": "task-id",
      "agent_id": "agent-id",
      "issue_id": "issue-id",
      "status": "running",
      "started_at": "2026-08-27T00:00:00Z"
    }
  ]
}
```

The response must not contain terminal task history, results, errors, work
directories, branch names, task briefs, or attribution details.

Move last-outcome data to a lazy endpoint:

```http
GET /api/agents/{id}/latest-outcome
```

Only the Squad hover card and Agent detail surfaces that render last activity
should call it. A batch form may be added for the full Agents page if measured
UI behavior shows that one request per expanded Agent is inefficient.

#### Realtime authorization and visibility constraints

The snapshot endpoint filters rows per actor through `accessibleAgentIDs`
(`server/internal/handler/agent.go`), while task lifecycle WebSocket events
are today a workspace-wide fanout that carries no creator or Agent-visibility
context. An existing security decision (PR #5018 / MUL-4159, documented in
`packages/core/realtime/use-realtime-sync.ts`) already forbids writing
cross-user caches directly from these fanout payloads for exactly this
reason. Any event-driven presence design must resolve this explicitly; the
naive version — broadcast events carrying a task projection, applied directly
by a client reducer — is not acceptable because it both leaks private-Agent
activity (`agent_id`, `issue_id`, `status`) to members who cannot see the
Agent and writes rows into a cache that the filtered snapshot endpoint would
never return.

Lifecycle filtering alone is insufficient. Direct Chat sessions are
creator-owned, and their HTTP handlers require both creator ownership and
current Agent access. The realtime path must preserve the same boundary for
`chat:message`, `chat:done`, and every other `chat:*` event tied to that
session. Transcript-bearing `task:message`, progress summaries, and transient
`task:activity` must likewise be restricted to connections authorized for the
linked task; a workspace-wide delivery followed by a client cache guard is not
authorization.

A single global workspace revision is also incompatible with per-connection
event filtering: events filtered out for an actor would appear as revision
gaps and trigger a full resync per private-Agent transition, reintroducing
the fan-out this design removes. Per-actor sequences assigned at send time
are not a good answer either: with the MUL-1138 horizontal-scaling
infrastructure (Redis Streams relay, multiple server nodes) already landed,
a per-actor monotonic counter would require a shared sequencer across nodes
and an atomicity story between the HTTP snapshot and in-flight WS events —
complexity out of proportion to the failure it guards against.

The chosen design deliberately avoids application-level sequencing and uses
three explicit routing classes:

- `workspace_agent`: lifecycle events for Issue tasks under the current
  workspace-scoped realtime contract. The receiving connection must belong to
  the workspace and see the Agent. Issue HTTP entitlement/window checks remain
  authoritative for opening the resource and for `task_scope` authorization.
- `user_agent`: all creator-owned direct Chat events, including the direct Chat
  task's lifecycle. The receiving connection's user ID must match the session
  creator and its actor must still see the Agent.
- `task_scope`: `task:message`, `task:progress`, and `task:activity`. The client
  subscribes only while it needs a live task timeline. The server authorizes the
  subscription against the linked Issue or creator-owned Chat plus Agent
  visibility before attaching the connection to the scope.

The implementation is:

1. Extract the Agent-visibility policy used by `accessibleAgentIDs` into a
   shared resolver. The HTTP snapshot and realtime connection setup call the
   same resolver so their authorization decisions cannot drift.
2. At connection setup, resolve the authenticated user, actor, and visible
   Agent IDs outside the Hub lock and attach them as connection authorization
   metadata. Public Agent creation, and hidden Agent Builder creation after its
   Chat session commits, re-run the same resolver and join only newly visible
   rooms in place. Permission, archive/restore, ownership, and
   workspace membership or role changes invalidate the metadata by closing
   affected connections; reconnect resolves it again and uses the clients'
   existing authoritative snapshot recovery.
3. Extend the event routing envelope with a required routing class and typed
   metadata, separate from client JSON: `workspace_id`, `agent_id`, and, as the
   class requires, `recipient_user_id`, `issue_id`, `chat_session_id`, or
   `task_id`. Producers derive this metadata from trusted task and session rows,
   not from client-supplied payload maps. Redis preserves it so every node can
   apply its local connection filter without decoding the public frame.
4. Add client task-scope subscription and unsubscribe frames. Web, desktop, and
   mobile subscribe before the initial or delta REST transcript fetch,
   unsubscribe when the timeline is no longer live, and replay active
   subscriptions before the reconnect catch-up fetch. The server's scope
   authorizer resolves task linkage and access before changing Hub membership;
   a rejected subscription returns an authorization-safe error and delivers no
   buffered or future task frames. REST rows and concurrent frames merge by
   `seq`, so subscribing before fetching closes the fetch/stream gap without a
   server-side event buffer.
5. Replace the generic non-personal `SubscribeAll` fallback for every `task:*`
   and `chat:*` event. Each known event must select one of the three routing
   classes. An unknown or unclassified event is not serialized externally; it
   increments a bounded event-type metric and emits a structured diagnostic
   without payload content.
6. The Hub hot path performs only in-memory identity, Agent-membership, and
   resource-scope membership checks before writing
   to a connection's send channel. It must not decode JSON, call the database,
   or invoke the visibility resolver while holding `h.mu.RLock`.
7. A lifecycle client event carries the minimal authoritative task projection:
   the
   presence fields listed above and nothing more.
8. Within a live connection, WebSocket delivery is ordered; the client trusts
   in-order application of events and does no gap arithmetic. Transport
   conditions that can lose events — disconnect, hub-initiated close on a slow
   consumer, node failover — surface to the client as a reconnect, and
   reconnect triggers exactly one full presence snapshot. A bounded
   low-frequency reconciliation timer remains as the safety net for silent
   divergence.

The frontend reducer should:

1. add or update a task on queued, dispatched, running, or waiting events;
2. remove it from presence on completed, failed, or cancelled events;
3. apply events idempotently so a duplicate delivery cannot corrupt state;
4. request one full presence snapshot on mount and after every reconnect.

Normal task lifecycle events should no longer invalidate the entire presence
snapshot.

If per-connection lifecycle projection is judged too large for the phase, the
acceptable fallback is a content-free notification: events name only the Agent
ID after delivery-layer visibility filtering, and the client refetches that
Agent's presence rows lazily. This keeps the lifecycle leak closed and still
eliminates whole-snapshot refetches, at the cost of one small request per
visible transition. There is no workspace-broadcast fallback for creator-owned
Chat or transcript content: those events must use `user_agent` or `task_scope`,
or be suppressed until the authorized delivery path is available.

The legacy `/api/agent-task-snapshot` route remains available for installed
clients until migration telemetry shows it is no longer needed.

### C. Issue List Projection

Define an `IssueListItem` response separate from the detail `IssueResponse`.
The base list projection should contain:

- identity, number, identifier, and title;
- status and status category;
- priority and assignee identity;
- parent and project identity;
- position and relevant dates;
- revision and last activity time;
- a bounded description preview; and
- label summaries needed by the current view.

Full description, attachments, reactions, and unrestricted metadata/property
objects should be omitted unless explicitly requested by a documented view
projection.

The server may expose a board bootstrap endpoint that returns all seven
category buckets in one response, including a separate cursor and total for
each bucket. This is optional after projection work: projection reduction must
ship first because request consolidation alone does not reduce the dominant
response bytes.

Existing granular Issue WebSocket cache patches should be retained. Reconnect
revalidation should target active query shapes instead of indiscriminately
fetching every cached Issue list.

### D. Lazy and Incremental Task Transcripts

Extend the frontend API client to accept `since`:

```text
listTaskMessages(taskId, { since })
```

When a cache already contains messages, reconciliation should send the highest
stored sequence and merge only newer messages. The merge must remain ordered,
deduplicated, and safe when an HTTP response races a WebSocket append.

For completed historical chat messages:

- render the persisted final `message.content` immediately;
- do not fetch the full task timeline merely because the message is visible;
- fetch tool and reasoning details only when the user expands the transcript;
  and
- use cursor pagination or bounded pages for a transcript with no warm cache.

The server should support a bounded initial page and bidirectional continuation
without deleting or truncating persisted records. Large tool input/output
objects should be returned in explicit detail chunks when they exceed the
normal page budget.

### E. Server-Side Inbox Grouping and Pagination

Replace the active Inbox list contract for new clients with a server-grouped
projection that returns the newest active row per `issue_id`. Items without an
Issue continue to group by their own ID.

The response should be cursor-paginated and should preserve the current rule
that active and archived Issue groups are mutually exclusive. Unread totals
should use a dedicated server aggregation rather than depend on downloading
the full list.

Inbox WebSocket handlers should patch, insert, remove, or move the affected
group when the event payload is authoritative. A full invalidation is the
fallback for incomplete events and reconnect recovery, not the default for
every Inbox mutation.

### F. Compression and Conditional Requests

Compression is an early egress fix, not a substitute for source reduction.
The application router installs no compression middleware and `writeJSON`
emits uncompressed `Content-Length`, so unless production ingress compresses,
every byte in the sample is a wire byte. During Phase 0B measurement:

- verify whether production ingress already applies gzip or Brotli;
- if absent, enable compression at the ingress, or add
  `chi/middleware.Compress` for JSON responses in the application router —
  whichever the deployment can roll out faster;
- expose actual wire bytes in ingress telemetry so compressed and
  uncompressed reductions are tracked separately.

The working hypothesis — to be validated in Phase 0C, not assumed — is that
this redundant JSON compresses at a high ratio (comparable JSON payloads
often see 80–90% reduction). Rollout must verify: the measured compression
ratio per route, server or ingress CPU overhead at production concurrency,
that no proxy in the chain double-compresses, and that every supported
client (web browsers, installed Electron builds, mobile) sends
`Accept-Encoding` and decodes the chosen encoding correctly. Compression
does not replace projection and refetch work — uncompressed bytes still cost
database reads, serialization, transfer buffering, and client parsing — but
it must not wait behind that work.

Conditional requests are limited to sanitized projections:

- weak ETags (`W/"<hash>"`, matching the existing precedent in
  `server/internal/handler/daemon_workspace.go`) plus
  `Cache-Control: private, no-cache` may be used on the summary and presence
  projections, whose payloads are stripped of secrets and detail-only data;
  `no-cache` permits the browser to store the response for revalidation,
  which is acceptable only for these sanitized bodies;
- weak, not strong, validators are required because compression ships first
  (Phase 0C): RFC 9110 §8.8.3 requires a strong ETag to differ between
  representations that differ in content-coding, so an application-generated
  strong ETag combined with ingress gzip/Brotli would assert byte equality
  that does not hold. Weak validators assert semantic equivalence and stay
  correct across encodings; responses must also send
  `Vary: Accept-Encoding`;
- the legacy full Agent list (MCP and runtime configuration) and task
  snapshot (`result`, `error`, work directories) must NOT get ETag +
  `no-cache` — deliberately writing those bodies into browser disk cache is
  a regression; they get `Cache-Control: no-store` instead (Phase 0A);
- validators must include the effective authorization and projection context;
- never use shared public caching for authenticated workspace data.

The marginal value of ETags shrinks once Phases 1–2 remove the refetch storm;
reassess retention in Phase 5. Conditional requests are a fallback for
redundant revalidation, not a reason to preserve broad invalidation behavior.

## Observability Design

### Structured Access Logs

Record the following for API responses:

- request ID;
- normalized route and a path passed through the existing
  `redactWebhookPath` protection;
- status and duration;
- uncompressed response bytes;
- workspace ID and internal user ID;
- client platform, version, and operating system;
- result row count where available;
- query shape, such as archived inclusion, status category, and page size; and
- request cause when the client can provide it: initial load, focus, reconnect,
  realtime event, manual refresh, or pagination.

Never log the unredacted path of a credential-bearing route or a raw query
string. Query shape is emitted only from an explicit allow-list of non-secret
fields, such as archive state, status category, page size, projection, and
request cause.

Names and email addresses must not be logged for attribution. Secret-bearing
request or response bodies must never be logged.

### Metrics

Add response-size histograms for normalized API routes and counters for client
request causes. Keep Prometheus labels low-cardinality:

- method;
- normalized route;
- status class;
- client platform; and
- a bounded client-version family.

Do not use workspace IDs, user IDs, names, emails, task IDs, or request IDs as
Prometheus labels. Detailed correlation belongs in structured logs.

Recommended dashboards:

1. total response bytes by route;
2. requests, average bytes, and p95 bytes by route;
3. response bytes per active workspace user-hour;
4. presence requests per task lifecycle event and active client;
5. Agent summary bytes by active and archived row count;
6. task transcript full-load versus delta-load bytes;
7. WebSocket reconnect and safety-net divergence recovery rate;
8. WebSocket egress bytes by bounded `task:*` and `chat:*` event type; and
9. accepted/rejected task-scope subscriptions and fail-closed event drops by
   bounded reason.

## Compatibility Strategy

The API must use an additive migration:

1. Add summary, presence, latest-outcome, paginated transcript, and grouped
   Inbox contracts.
2. Release web and desktop clients that prefer the new contracts.
3. Track old-route requests by authenticated client version.
4. Keep legacy routes functionally unchanged during the support window.
5. Remove or narrow legacy routes only through a separate compatibility
   decision after supported installed clients have migrated.

WebSocket capability negotiation is additive. Current clients send
`task_scopes=1` and receive transcript content through authorized task scopes.
Installed desktop clients that predate task-scope subscriptions join separate
legacy Agent rooms derived from the same connection-time Agent visibility and
Chat-creator checks. The server dual-routes transcript frames to task scope and
the applicable legacy Agent room during the support window; it never restores
workspace-wide transcript fanout. Authenticated REST remains the authoritative
reconciliation path for both generations.

Changing the default behavior of `/api/agents` or removing terminal rows from
`/api/agent-task-snapshot` in place is not acceptable because existing desktop
clients explicitly depend on those contracts.

## Security and Privacy Requirements

- Every new endpoint must reuse existing workspace membership and Agent
  visibility authorization.
- Summary projections must not widen access to private Agents.
- Realtime task lifecycle events that carry payload data must be filtered per
  connection with the same Agent-visibility resolution as the snapshot
  endpoint. Direct Chat lifecycle events also require session-creator identity.
- `chat:*` content for a creator-owned direct Chat must be delivered only to
  connections for that creator whose actor still sees the Agent.
- `task:message`, `task:progress`, and `task:activity` must be delivered only to
  an authorized `task_scope` subscription or an authorization-filtered legacy
  Agent compatibility room. A client-side cache guard is not an authorization
  control, and workspace-wide transcript delivery is prohibited.
- Broadcasting task projections, transcript content, or creator-owned Chat
  content workspace-wide is prohibited. This extends the existing PR #5018 /
  MUL-4159 decision from "do not write cross-user caches from fanout payloads"
  to "do not fan out visibility-scoped or owner-scoped payloads at all".
- Every externally serialized `task:*` and `chat:*` event must have an explicit
  routing class and public payload contract. Unknown or unclassified event types
  fail closed and are observable without logging their payloads.
- Realtime fanout performs no database or network I/O while holding the Hub
  lock. Authorization is precomputed at connection setup. Additive Agent
  creation refreshes visibility outside the lock and joins only newly allowed
  rooms in place; narrowing permission or membership changes close affected
  connections so reconnect resolves a fresh set.
- Agent configuration secrets must remain excluded from list and realtime
  payloads.
- ETags and caches must be private to the authorization context.
- Response-size logging must record byte counts, not response bodies.
- Access logging must preserve `redactWebhookPath` and must not record raw query
  strings or unapproved query values.
- Workspace and user identifiers may appear in controlled structured logs but
  must not become high-cardinality metric labels.
- Pagination cursors must be opaque, validated, and scoped to the active query
  and workspace.

## Implementation Sequence

### Phase 0A: Immediate Security Containment

This phase starts immediately and is not gated by the seven-day traffic
baseline. The HTTP baseline in Phase 0B can run in parallel because this phase
changes WebSocket disclosure, not the five measured HTTP route contracts.

1. Replace the outbound `task:*` and `chat:*` boundary with an explicit
   event-contract registry, not a wider `internalOnlyPayloadKeys` blocklist. A
   blocklist cannot contain `task:dispatch`, whose payload is seeded from the
   entire persisted `task.Context` (including the Quick Create `prompt`,
   `requester_id`, and `attachment_ids`); the safe boundary is "serialize only
   the fields the public contract names". The task contracts are:

   - `task:queued`, `task:running`, `task:completed`, `task:failed`, and
     `task:cancelled`: `task_id`, `issue_id`, `status`, and optional
     `chat_session_id`;
   - `task:waiting_local_directory`: `task_id`, `issue_id`, `status`, optional
     `chat_session_id`, and optional sanitized `wait_reason`;
   - `task:dispatch`: `task_id`, `issue_id`, `runtime_id`, and optional
     `chat_session_id` — the `task.Context` expansion is removed entirely;
   - `task:progress`: `task_id`, `summary`, and optional `step` and `total`; it
     carries no `issue_id` today, so authorization comes from `task_scope`, not
     payload inspection;
   - `task:message`: `task_id`, optional `issue_id`, `seq`, `type`, and optional
     `tool`, `content`, `input`, `output`, and `created_at`;
   - `task:activity`: `task_id`, optional `issue_id`, `activity`, and
     `after_seq`; clients continue to accept an omitted `after_seq` from older
     servers.

   `agent_id` is routing metadata and is excluded from every public task
   payload; no current client reads it. `issue_id` remains in lifecycle and
   transcript contracts because mobile Issue realtime self-gates on
   `payload.issue_id === issueId`. `wait_reason` remains because web and desktop
   render the parked-task reason. The TypeScript contracts and mobile payload
   casts must change in the same commit as the server registry.

   Every known `chat:*` event (`chat:message`, `chat:done`,
   `chat:quick_actions`, `chat:cancel_finalized`, `chat:session_read`,
   `chat:session_updated`, and `chat:session_deleted`) serializes only its
   existing typed public protocol fields and is classified `user_agent`.
   Unknown, malformed, or unclassified `task:*` and `chat:*` events fail closed:
   they produce no external frame, increment a bounded metric keyed by approved
   event type or `unknown`, and emit no payload content in logs.
2. Implement the three routing classes, trusted routing metadata, shared
   Agent-visibility resolver, connection authorization metadata,
   Redis relay envelope, and I/O-free Hub checks defined in Proposal B. Direct
   Chat tasks use `user_agent`; workspace-visible Issue lifecycle events use
   `workspace_agent`; message, progress, and activity events use `task_scope`.
3. Add task-scope subscribe/unsubscribe support to web, desktop, and mobile,
   including authorization before Hub membership, capability signaling, and
   subscription replay after reconnect. A supported legacy client that omits
   the capability joins only visibility-filtered compatibility rooms, while
   creator-targeted `chat:*` events continue to arrive without a task
   subscription.
4. Add local-Hub and Redis-relay tests proving that unauthorized connections
   receive neither private-Agent lifecycle events nor another creator's Chat or
   task transcript content.
5. Confirm authorized clients retain lifecycle invalidations, creator-targeted
   Chat updates, and subscribed task timelines. Authorization invalidation
   closes affected connections. On reconnect, the server resolves fresh
   authorization, the client replays active subscriptions, then authenticated
   snapshot and incremental REST recovery reconcile concurrent frames by
   identity and `seq`. A rejected subscription or REST authorization failure
   removes the corresponding stale client cache.
6. Add explicit `Cache-Control: no-store` to legacy responses that carry
   sensitive detail: the full Agent list with MCP/runtime configuration and the
   task snapshot with `result`, `error`, and work-directory data.

### Phase 0B: Measurement Baseline

1. Capture the production deployment commit and client-version distribution.
2. Add generic response-byte logging and route-level size metrics while
   preserving path redaction.
3. Confirm ingress compression and distinguish upstream bytes from wire bytes.
4. Establish a seven-day baseline for the five HTTP routes. Phase 0A does not
   wait for this window to finish.

### Phase 0C: Verified Early Egress Improvements

After current ingress behavior has been captured:

1. Enable response compression at the ingress or application middleware and
   validate it per Proposal F: measured ratio, CPU overhead, proxy
   double-compression, and the client `Accept-Encoding` matrix.
2. Wire the existing `since` capability into `listTaskMessages` so warm
   transcript reconciliation transfers only newer messages. The server query
   already exists; only the client changes.
3. Re-baseline the five routes. Later phases are justified against
   post-compression wire bytes and uncompressed response bytes, not only the
   original sample.

Narrowing the global prefetch to active Agents is not an early quick win. The
Agents page derives archive counts and rows from the archived-inclusive query,
and Dashboard needs archived identities to distinguish archived spend from
hard-deleted spend. The narrowing therefore ships with the Phase 1 contract
and consumer migration.

### Phase 1: Agent Summary

1. Add the summary SQL projection, `projection=summary` handler path, schema,
   and client parser.
2. Implement the `archive_state` filter and page envelope, then give the Agents
   archive scope and Dashboard historical attribution separate query keys.
3. Move global workspace prefetch and mention surfaces to the summary query
   using `archive_state=active`.
4. Load Agent configuration only on demand.
5. Add direct realtime summary cache patches where event payloads are complete
   and visibility-safe.
6. Add the weak ETag + `Cache-Control: private, no-cache` validators for the
   summary projection (Proposal F).

This phase targets the route responsible for 51.7% of the measured sample.

### Phase 2: Presence and Realtime Reduction

1. Add the minimal presence endpoint (visibility filtering for lifecycle
   events ships earlier, in Phase 0A).
2. Add the lazy latest-outcome endpoint.
3. Migrate presence consumers.
4. Replace lifecycle invalidation with event-applied cache reduction: ordered
   in-connection delivery, idempotent reducers, full snapshot on mount and
   reconnect, bounded safety-net reconciliation.
5. Preserve full synchronization for initial load and reconnect.
6. Add the weak ETag + `Cache-Control: private, no-cache` validators for the
   presence projection (Proposal F).

The Agent summary and presence phases together address approximately 75.3% of
the measured sample (uncompressed).

### Phase 3: Task Transcript Transport

1. Make completed historical transcripts lazy (the `since` wiring itself
   ships in Phase 0C).
2. Add bounded initial pages and continuation for large histories.
3. Measure full-versus-delta response bytes.

### Phase 4: Issue and Inbox Projections

1. Introduce and migrate to `IssueListItem`.
2. Evaluate board request consolidation after field projection.
3. Add grouped, paginated Inbox contracts and aggregate unread counts.
4. Replace broad Inbox invalidations with item/group cache patches.

### Phase 5: Consolidation

1. Tune compression settings against measured wire bytes.
2. Reassess whether ETags introduced with the sanitized summary and presence
   projections in Phases 1 and 2 still pay for themselves once refetch
   reduction has landed.
3. Reassess legacy endpoint retention using client-version telemetry.

## Testing Requirements

### Server Tests

- Agent summary excludes all detail-only and secret-bearing fields.
- Agent summary preserves current visibility rules for owner, admin, member,
  and Agent actors.
- `archive_state=active`, `archive_state=archived`, and `archive_state=all`
  return the correct disjoint scopes, and the first-page counts use the same
  visibility filter as the items.
- Summary cursors preserve stable `(created_at, id)` ordering without duplicate
  or missing items across pages.
- Presence contains active tasks only and never serializes terminal result or
  error data.
- Each `task:*` event serializes exactly its registered public fields.
  `task:waiting_local_directory` retains optional sanitized `wait_reason`;
  `task:dispatch` retains `runtime_id` but no `task.Context` field (`prompt`,
  `requester_id`, and `attachment_ids` must not appear on the wire); and
  `agent_id` appears only in trusted routing metadata, never client JSON.
- Each known `chat:*` event serializes exactly its typed protocol fields and is
  routed with trusted session creator and Agent metadata. Unknown, malformed,
  or unclassified `task:*` and `chat:*` events produce no WebSocket frame and
  emit only bounded, payload-free observability.
- Summary and presence conditional requests: a repeated request returns 304
  on an unchanged body and 200 after a change; an authorization-scope change
  (Agent visibility) changes the validator; the validator behaves correctly
  under both gzip/Brotli and identity encodings with `Vary: Accept-Encoding`.
- Task lifecycle events for a private Agent are not delivered through either
  the local Hub or Redis relay to a connection whose actor cannot see that
  Agent. A direct Chat task's lifecycle is delivered only to its creator.
- `chat:*` events are never delivered to a different session creator, including
  when both users can see the same Agent.
- `task:message`, `task:progress`, and `task:activity` are delivered only after
  an authorized task-scope subscription or through an authorization-filtered
  legacy Agent room; rejected, unsubscribed, unauthorized, and disconnected
  scopes receive nothing through either local or Redis delivery.
- Public Agent creation and committed Agent Builder session creation expand
  visible rooms in place. Permission, ownership, archive, restore, and
  workspace-role changes close affected connections and resolve a fresh set on
  reconnect.
- Latest outcome respects Agent visibility.
- Issue list projections return only documented fields.
- Task message `since`, page boundaries, and continuation are deterministic.
- Inbox grouping returns one newest item per Issue and preserves active/archive
  exclusivity.
- Cursor tampering and cross-workspace cursor reuse are rejected.
- Sensitive legacy Agent and snapshot routes return `Cache-Control: no-store`.
- Access logging redacts credential-bearing Webhook paths and does not emit raw
  query strings or unapproved query values.

### Frontend Tests

- Workspace mount does not request archived or detail-grade Agent data.
- The task event TypeScript contracts in `packages/core/types/events.ts`
  match the per-event public payloads (`agent_id` removed from the required
  fields and `wait_reason` preserved), and the mobile task-event cast compiles
  against the same contract.
- Web, desktop, and mobile create and remove task-scope subscriptions with the
  live transcript lifecycle, replay active subscriptions once after reconnect,
  and do not render frames received outside an active scope.
- Agent configuration surfaces fetch detail only when opened.
- The summary decoder accepts a legacy `Agent[]` only on the initial cursorless
  request, filters and projects it locally, records
  `old_backend_full_agents`, and stops pagination.
- A legacy array returned for a non-empty cursor is rejected as a protocol
  error.
- A malformed response matching neither the page envelope nor legacy array is
  rejected instead of being converted to an empty list.
- Archive and historical-attribution surfaces use explicit archive-aware query
  keys rather than the active workspace-prefetch key.
- Presence reducers handle duplicate and terminal events idempotently.
- A reconnect triggers exactly one authoritative resync.
- Duplicate event delivery leaves presence state unchanged (idempotence).
- Completed chat messages do not fetch a transcript until expanded.
- Transcript delta reconciliation does not lose a concurrent WebSocket event.
- Issue and Inbox pagination retain current sorting, grouping, and optimistic
  update behavior.

### End-to-End Tests

- Web and desktop clients show correct Agent presence during a full task
  lifecycle.
- A member without visibility into a private Agent never receives that
  Agent's task lifecycle payloads and never renders its presence.
- Two members who can see the same Agent never receive each other's direct Chat
  content or Chat-task lifecycle events.
- A task transcript streams to an authorized subscribed client, stops after
  unsubscribe, and catches up through the authenticated incremental REST route
  after reconnect.
- Presence recovers after a forced WebSocket disconnect.
- Archived Agents remain accessible from the archive view.
- Large task transcripts load incrementally and remain complete.
- Board, list, table, and Inbox critical flows preserve current behavior.
- A new client renders active and archived Agent scopes correctly against a
  simulated older backend that returns the legacy `Agent[]` response.
- A supported legacy client continues to work against the unchanged legacy
  routes, receives live transcript frames only through its authorization-
  filtered compatibility rooms, and recovers a complete transcript through
  REST.

## Acceptance Criteria

Measured in a representative production workspace after rollout. Size
criteria refer to uncompressed response bytes unless stated otherwise; the
egress criterion (9) refers to wire bytes.

0. Compression is verified active end to end, and wire bytes and uncompressed
   bytes are reported separately per route.
1. `/api/agents?projection=summary` p95 response size is below 100 KiB and at
   least 80% smaller than the legacy Agent list for the same actor. Active and
   archived pages are disjoint, first-page counts reconcile with the same
   visibility scope, and pagination neither duplicates nor drops Agents.
2. `/api/agent-presence` p95 response size is below 30 KiB under normal active
   task counts.
3. Normal task lifecycle events do not trigger a full presence request on every
   state transition.
4. Presence performs one full fetch on initial mount and at most one recovery
   fetch per reconnect.
5. Historical completed chat messages generate no task-message request until a
   user opens the transcript.
6. Warm transcript reconciliation transfers only messages newer than the
   highest cached sequence.
7. The Issue list projection reduces p95 bytes per returned row by at least 50%
   without increasing the number of first-screen requests.
8. Active Inbox payload is bounded, server-grouped, and cursor-paginated.
9. Total wire bytes for the five measured routes decrease by at least 70% per
   active user-hour, while error rate and p95 latency do not regress
   materially. WebSocket egress added by payload-carrying events is included
   in this measurement.
10. No authorization, Agent visibility, or transcript completeness regression
    is observed, and installed clients retain their existing authenticated HTTP
    contracts. In particular, no connection receives a task lifecycle payload
    for an Agent its actor cannot see, no user receives another creator's direct
    Chat events, and no unsubscribed or unauthorized connection receives task
    transcript content. A legacy client without task-scope subscriptions
    receives live frames only through its visibility-filtered compatibility
    room and still reconciles the complete transcript through authenticated
    REST.

## Risks and Mitigations

| Risk | Mitigation |
|---|---|
| Installed clients depend on current HTTP and WebSocket behavior | Retain legacy HTTP contracts; keep creator-targeted Chat and authorized lifecycle events; dual-route task content only through authorization-filtered compatibility rooms |
| Payload-carrying events leak private-Agent activity or creator-owned Chat/transcript content (exists today) | Immediate Phase 0A event registry plus `workspace_agent`, `user_agent`, and `task_scope` routing; local, Redis, and E2E negative-delivery tests |
| A legacy client does not implement task-scope subscriptions | Join only authorization-filtered legacy Agent rooms, dual-route during the support window, and retain authenticated REST reconciliation; never restore workspace-wide content fanout |
| Realtime cache diverges after a missed event | Reconnect-triggered full snapshot (loss conditions surface as reconnects) plus a bounded safety-net reconciliation for silent divergence |
| Multi-node delivery reorders or drops events | Filtering and delivery stay per-connection at the owning edge; transport loss and visibility invalidation resolve through reconnect resync, and bounded reconciliation covers silent divergence |
| Summary omits a field required by an unexpected surface | Inventory consumers, use typed schemas, and retain detail queries |
| Request consolidation produces one very large response | Narrow projections first and retain per-bucket cursors |
| Transcript pagination loses ordering or concurrent events | Sequence-based merge, idempotent reducers, and race-condition tests |
| Compression hides application-level amplification | Track uncompressed and wire bytes separately |
| Metrics create cardinality or privacy problems | Restrict metric labels and keep identifiers in controlled logs only |
| Legacy routes remain indefinitely | Track requests by client version and define a separate retirement gate |

## Expected Outcome

Phase 0A closes the existing lifecycle, direct Chat, and transcript broadcast
authorization defects without waiting for traffic measurement. Phases 0B and
0C establish the baseline and deliver measured
near-term wire reductions. Phases 1 and 2 remove the largest amplification
loop: full Agent and task-snapshot responses repeatedly downloaded by every
active workspace client. Later phases bound the remaining list and transcript
responses.

Compression and transcript deltas reduce near-term wire bytes; archive-aware
Agent summary and presence contracts then remove bytes and requests at the
source. Neither layer substitutes for the other: compression does not fix
request amplification or serialization cost, and projections do not remove
the redundancy that compression eliminates.
