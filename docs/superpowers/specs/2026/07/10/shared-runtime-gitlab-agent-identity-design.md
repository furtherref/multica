# Shared Runtime GitLab Agent Identity - Design

Date: 2026-07-10
Status: Approved

Implementation plan:
`docs/superpowers/plans/2026/07/10/shared-runtime-gitlab-agent-identity-plan.md`

## Goal

Configure the shared Copilot runtime on `furtherref-agent1` so every real
Multica Agent task:

1. requires the Agent's existing `custom_env.GITLAB_TOKEN`;
2. validates that token against `https://gitlab.tigermed.net/api/v4/user`
   before the real Copilot process starts;
3. derives Git commit author and committer identity from the validated GitLab
   account; and
4. uses that same Agent token for Git-over-HTTPS operations without changing
   process-global or repository-global Git configuration.

This solves identity collisions on a shared root-owned runtime while keeping
the change local to this server. Multica backend, frontend, database schema,
and checked-in runtime code remain unchanged.

## Current State

The following behavior was verified against Multica `v0.3.42` and the live
runtime:

- Agent `custom_env` is included in the task claim and injected into the
  provider subprocess. The active Copilot task receives `GITLAB_TOKEN`.
- The active task has no `GIT_AUTHOR_NAME`, `GIT_AUTHOR_EMAIL`,
  `GIT_COMMITTER_NAME`, or `GIT_COMMITTER_EMAIL`.
- The checked-out worktrees have no repository `user.name` or `user.email`.
- The root account has no global `user.name` or `user.email`.
- GitLab HTTPS authentication currently falls back to the root account's
  shared credential helper, backed by `/root/.git-credentials`.
- Multica's bare-cache clone and fetch operations run in the daemon process
  environment, before the Agent provider process is started. Agent
  `custom_env` therefore cannot replace the daemon's cache credential.
- The active Agent token currently resolves to GitLab user `qiang.zhang`.
- The real provider entry point is
  `/usr/local/lib/nodejs/node-v22.23.1-linux-x64/bin/copilot`, currently
  reporting GitHub Copilot CLI `1.0.70`.

`GITLAB_TOKEN` alone does not set commit identity. Git author/committer
metadata and GitLab HTTPS authentication are separate controls and must both
be configured for each task process.

## Locked Decisions

1. **Server-only change.** Do not modify Multica application code, API,
   database, frontend, or release artifacts.
2. **Copilot only.** Apply the policy to the Copilot runtime on this host by
   setting Multica's existing `MULTICA_COPILOT_PATH` override to a wrapper.
3. **Agent identity.** The GitLab account represented by the Agent's token is
   the canonical commit author and committer. The human who invoked that Agent
   does not change the Git identity for the task.
4. **Existing secret surface.** Continue storing the token in Agent
   `custom_env`. The accepted limitation is that `custom_env` is plaintext
   JSONB at rest and is visible to the Agent process.
5. **Fail closed.** A real task does not start Copilot when the token is
   missing, rejected by GitLab, inactive, or lacks a usable name/email.
6. **Validation level.** Validate token identity with `/api/v4/user`. Do not
   preflight per-repository or protected-branch write permission.
7. **Token scopes.** The least-privilege recommendation is `read_user` plus
   `write_repository`. `read_user` supports `/user`; `write_repository`
   supports pull/push but does not itself authorize API calls. A broader token
   such as `api` may also satisfy both operations. The wrapper validates
   behavior, not exact scope names, because this GitLab version does not expose
   a reliable self-scope introspection endpoint.
8. **No persistent task credentials.** Do not put the Agent token in remote
   URLs, Git config files, credential-store files, command arguments, logs, or
   generated worktree files.
9. **Cache authentication remains separate.** Keep the daemon's existing
   shared credential available for bare-cache clone/fetch. The wrapper only
   replaces credentials in the Copilot task process and its descendants.
10. **No global Git mutations.** Never run task-dependent `git config
    --global`, rewrite `/root/.git-credentials`, or set identity in the shared
    bare repository.

## Server Layout

Install a root-owned deployment under:

```text
/opt/multica-gitlab-identity/
  bin/
    copilot-wrapper
    git-credential-multica-gitlab
  etc/
    runtime.conf
```

Permissions:

| Path | Owner | Mode | Contents |
|---|---|---:|---|
| `/opt/multica-gitlab-identity` | `root:root` | `0755` | Deployment root |
| `bin/copilot-wrapper` | `root:root` | `0755` | Task preflight and real Copilot exec |
| `bin/git-credential-multica-gitlab` | `root:root` | `0755` | Read-only credential helper |
| `etc/runtime.conf` | `root:root` | `0644` | Non-secret host and executable paths |

`runtime.conf` contains no token:

```text
GITLAB_BASE_URL=https://gitlab.tigermed.net
GITLAB_HOST=gitlab.tigermed.net
REAL_COPILOT_PATH=/usr/local/lib/nodejs/node-v22.23.1-linux-x64/bin/copilot
```

The exact real Copilot path is captured during installation and verified to
be executable before activation. The wrapper must reject a configuration that
points back to itself.

## Daemon Activation

Persist the provider override in a root-owned profile file:

```text
/etc/profile.d/multica-gitlab-identity.sh
```

with:

```sh
export MULTICA_COPILOT_PATH=/opt/multica-gitlab-identity/bin/copilot-wrapper
```

The daemon must be restarted from an environment that has sourced this file.
Activation is successful only when:

- the daemon process environment contains the expected
  `MULTICA_COPILOT_PATH`;
- `multica daemon status` reports `running`;
- the registered Copilot runtime is online; and
- the daemon's provider version probe succeeds through the wrapper.

This design does not add daemon boot autostart. It preserves the current
daemon lifecycle and only changes the Copilot executable selected at daemon
startup.

## Wrapper Control Flow

`copilot-wrapper` is a Bash wrapper that ends with `exec` so the real Copilot
process keeps normal stdin/stdout/stderr and signal behavior.

### Probe path

When `MULTICA_TASK_ID` is absent, immediately execute the real Copilot binary
without requiring `GITLAB_TOKEN`.

This path is required for Multica's provider version and model probes, which
run outside a task and therefore do not receive Agent `custom_env`.

`MULTICA_TASK_ID` is a reliable task marker because Multica injects it for
actual task runs and blocks Agent `custom_env` from overriding `MULTICA_*`
variables.

### Task path

When `MULTICA_TASK_ID` is present:

1. Require a non-empty `GITLAB_TOKEN`.
2. Request `<GITLAB_BASE_URL>/api/v4/user` with the token in a
   `PRIVATE-TOKEN` header.
3. Keep the token out of command arguments. Supply the header through a
   private pipe/process substitution; do not consume the Copilot process's
   stdin.
4. Use strict network bounds: short connect timeout, total timeout, one retry
   only for connection errors or HTTP 5xx.
5. Require HTTP 200 and valid JSON containing:
   - positive `id`;
   - non-empty `username`;
   - `state == "active"`;
   - non-empty `name`; and
   - non-empty `email`.
6. Reject identity fields containing NUL, CR, or LF. Trim surrounding
   whitespace without rewriting valid Unicode names.
7. Export the validated identity and task-scoped credential configuration.
8. `exec` the real Copilot binary with the original arguments unchanged.

The wrapper must never print the API response body. Safe diagnostics may
include the task ID, failure class, GitLab host, and HTTP status, but never the
token, authorization header, name/email response payload, or remote URL with
credentials.

## Commit Identity

For a validated GitLab profile, the wrapper exports:

```text
GIT_AUTHOR_NAME=<GitLab /user.name>
GIT_AUTHOR_EMAIL=<GitLab /user.email>
GIT_COMMITTER_NAME=<GitLab /user.name>
GIT_COMMITTER_EMAIL=<GitLab /user.email>
```

These process-scoped variables take precedence over system, global, shared
bare-repository, and worktree Git identity configuration. They naturally
propagate to Git commands launched by Copilot while remaining isolated from
concurrent tasks.

The wrapper overwrites any same-named values already present in Agent
`custom_env`; the validated GitLab profile is authoritative.

## GitLab Credential Helper

`git-credential-multica-gitlab` implements the standard Git credential-helper
protocol.

For the `get` operation it:

1. parses `protocol` and `host` from stdin;
2. returns nothing unless `protocol=https` and `host` exactly matches
   `gitlab.tigermed.net`;
3. requires non-empty `GITLAB_TOKEN`; and
4. writes only:

```text
username=oauth2
password=<GITLAB_TOKEN>
```

For `store`, `erase`, or unknown operations it exits successfully without
writing anything. It never creates a file and never logs a credential.

Before `exec`, the wrapper appends two environment-scoped Git config entries:

1. an empty `credential.https://gitlab.tigermed.net.helper` value to reset
   lower-priority helpers for that host; and
2. the absolute path to `git-credential-multica-gitlab` as the only active
   helper for that host.

If `GIT_CONFIG_COUNT` already exists and is a valid non-negative integer, the
wrapper preserves its entries and appends at the next indexes. An invalid
existing count fails closed instead of using ambiguous Git configuration.

Also export:

```text
GIT_TERMINAL_PROMPT=0
GIT_ASKPASS=/bin/false
```

This prevents an invalid Agent token from falling through to an interactive
prompt or the root account's shared credential. The root credential helper
remains unchanged and available to the daemon's separate bare-cache process.

## Cache and Worktree Boundary

The two credential paths intentionally remain separate:

```text
Daemon process
  -> shared root credential
  -> clone/fetch bare caches under WorkspacesRoot/.repos

Copilot task process
  -> Agent GITLAB_TOKEN via environment-scoped helper
  -> Git pull/push and glab operations launched by the Agent
```

The credential used to populate a bare cache does not define commit metadata
and does not need to match the task Agent. Worktrees share object storage with
the cache, but each Copilot process receives its own identity and credential
environment.

No task may rewrite `origin` to contain a token. Existing clean HTTPS remotes
remain unchanged.

## Failure Behavior

The wrapper exits before launching real Copilot in these cases:

| Failure | Safe message |
|---|---|
| Token missing/empty | `required Agent environment variable GITLAB_TOKEN is missing` |
| GitLab timeout/unavailable | `GitLab identity validation unavailable` |
| HTTP 401/403 | `GitLab token validation failed` |
| Other non-200 response | `GitLab identity validation returned HTTP <status>` |
| Invalid JSON/profile | `GitLab identity response is incomplete or invalid` |
| Real Copilot path invalid | `real Copilot executable is unavailable` |
| Invalid inherited Git config count | `task Git credential configuration is invalid` |

Use stable wrapper exit codes for operator diagnosis, but do not claim a new
Multica `failure_reason`: without product-code changes, Multica may surface the
provider launch failure as a generic task failure.

GitLab `/user` validation proves token validity and identity only. It does not
prove that the account can push every task repository or protected branch.
Repository membership, `write_repository`, and branch rules can still reject
the eventual push, which is an accepted boundary of validation level 2.

## Concurrency

All mutable values are process-local environment variables. The wrapper and
helper must not mutate:

- `/root/.gitconfig`;
- `/root/.git-credentials`;
- shared bare-repository config;
- worktree `.git/config`;
- system Git config; or
- remote URLs.

Concurrent Agents can therefore use different tokens, authors, and committers
without racing on shared files. Wrapper startup must not use a fixed temporary
filename; pipes and in-memory variables are preferred.

## Security Boundary

Accepted risks:

- `custom_env` is plaintext in the Multica database.
- The token is visible to the Agent process and to root through `/proc`.
- An Agent with shell execution can deliberately override environment values,
  use `git -c`, bypass a local helper, or invoke another Git binary.
- The wrapper is a reliable configuration and isolation control for normal
  Agent execution, not a hostile-code security sandbox.

If tamper-proof commit attribution is required later, enforce author/committer
policy in GitLab with a server-side push rule or pre-receive hook. Local hooks
and wrappers are bypassable by a process that controls its own commands.

Operational safeguards:

- Files are root-owned and not writable by task processes.
- Token values never enter argv, logs, Git URLs, Git config, or persistent
  helper storage.
- Validation errors redact response bodies.
- The helper is host-locked and ignores non-GitLab credential requests.
- The wrapper uses the email returned by authenticated `/user`, so GitLab can
  associate the commit with the same account when that address is verified on
  the instance.

## Deployment

1. Confirm the daemon is running and record its current PID, environment,
   Copilot path/version, runtime ID, and active-task count.
2. Record checksums/permissions for `/root/.gitconfig` and
   `/root/.git-credentials`; these files must not change during deployment.
3. Resolve and pin the real Copilot executable path.
4. Install the wrapper, helper, and non-secret config atomically under
   `/opt/multica-gitlab-identity`.
5. Run all wrapper/helper tests against a fake Copilot executable before
   changing `MULTICA_COPILOT_PATH`.
6. Install `/etc/profile.d/multica-gitlab-identity.sh`.
7. Wait until `multica daemon status` and `/health` report zero active tasks.
   Do not interrupt an Agent task to activate this change.
8. Restart the daemon from a shell that sources the profile file.
9. Verify the daemon environment, Copilot version probe, online runtime, and
   unchanged global Git files.
10. Run the live missing-token, invalid-token, and valid-token acceptance
    checks without exposing token values.

## Rollback

1. Wait for zero active tasks.
2. Remove or disable `/etc/profile.d/multica-gitlab-identity.sh`.
3. Restart the daemon with `MULTICA_COPILOT_PATH` unset so normal Copilot path
   discovery resumes.
4. Verify Copilot runtime registration and task launch.
5. Remove `/opt/multica-gitlab-identity` only after confirming no process uses
   it.

Rollback does not require restoring Git configuration because deployment never
modifies shared Git identity, remotes, or credential files.

## Testing and Acceptance

### Wrapper tests with a fake Copilot executable

- No `MULTICA_TASK_ID`, no token: version/probe invocation reaches real
  Copilot unchanged.
- Task ID present, token absent: wrapper fails and fake Copilot is not invoked.
- Task ID present, invalid token: wrapper fails and fake Copilot is not
  invoked.
- Task ID present, valid token: fake Copilot receives the four expected Git
  identity variables and the original argv.
- Incomplete `/user` profile: wrapper fails before launch.
- Real path points to wrapper or is not executable: wrapper fails without a
  recursion loop.
- Existing valid `GIT_CONFIG_COUNT`: wrapper preserves prior entries and
  appends its host-specific reset/helper entries.
- Existing invalid `GIT_CONFIG_COUNT`: wrapper fails closed.

### Credential-helper tests

- Exact GitLab HTTPS host + `get`: emits `oauth2` and the process token.
- Wrong host, wrong protocol, `store`, and `erase`: emit no credential.
- Missing token: emits no credential and fails the task-side credential fill.
- No test output or file contains the token after completion.

### Concurrency test

Run two wrapper processes concurrently against a local GitLab-user API stub,
with different synthetic tokens and profiles. Each fake Copilot process must
observe only its own identity and credential. Global Git config and credential
file checksums must remain unchanged.

### Live runtime acceptance

- `multica daemon status` reports running and the Copilot runtime is online.
- A probe with no task context still returns the installed Copilot version.
- A test task using an Agent without `GITLAB_TOKEN` fails before Copilot starts.
- A test task using an invalid token fails before Copilot starts and logs no
  secret or API body.
- A test task using a valid token can run `git var GIT_AUTHOR_IDENT` and
  `git var GIT_COMMITTER_IDENT`; both match the token's GitLab profile.
- A commit created in a task worktree reports the expected `%an <%ae>` and
  `%cn <%ce>`.
- `git credential fill` for `gitlab.tigermed.net` resolves through the
  task helper, while another host receives no credential from it.
- A controlled branch push is attributed by GitLab to the token account.
- Multica bare-cache fetch still succeeds using the daemon credential.
- `/root/.gitconfig`, `/root/.git-credentials`, bare remotes, and worktree
  remotes are unchanged.

## Non-Goals

- No per-invoking-user Git identity; identity belongs to the selected Agent.
- No GitLab OAuth connection flow.
- No encrypted replacement for Agent `custom_env`.
- No Runtime or Agent UI indicator for required environment keys.
- No repository/branch write-permission preflight.
- No GitLab server-side push policy.
- No change to non-Copilot providers or other servers.
- No daemon boot-autostart redesign.
