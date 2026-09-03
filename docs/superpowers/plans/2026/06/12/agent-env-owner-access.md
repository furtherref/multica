# Agent Env Owner Access Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Allow the agent's owner (`agent.owner_id`) to reveal & edit that agent's environment variables, in addition to workspace owner/admin — and make the frontend hide the "Reveal & edit" button from members who lack the permission.

**Architecture:** The backend gate `authorizeAgentEnv` (`server/internal/handler/agent_env.go`) currently requires workspace role owner/admin. We relax it to the same rule as `canManageAgent` / `canViewAgentSecrets`: workspace owner/admin **or** `agent.owner_id == userID`. The agent-actor denial (MUL-2600) and the fail-closed audit logging are **unchanged**. The frontend already has the exact matching rule (`canEditAgent` in `packages/core/permissions/rules.ts`) and the agent detail page already computes it (`useAgentPermissions → canEdit`) — we thread `canEdit.allowed` down to `EnvTab` and render a read-only state when false.

**Tech Stack:** Go (chi handlers, `go test` against a test DB), React + Vitest/jsdom in `packages/views`, i18n JSON locales (en / zh-Hans / ja / ko, enforced by `locales/parity.test.ts`).

**Why this is safe / consistent:**
- Any plain member can already create an agent **with** `custom_env` at creation time; today they just can't see or rotate their own values afterwards. This change closes that asymmetry.
- The backend rule becomes identical to `canManageAgent` (edit/archive) and `canViewAgentSecrets` (mcp_config redaction) — one coherent ownership model, mirrored 1:1 by the existing frontend rule `canEditAgent`.
- MUL-2600's security properties stay intact: agent actors are still denied before the role check, every reveal/edit still writes an audit row, and audit failure still blocks the response.
- `owner_id` can be NULL (ownerless agent) → only workspace owner/admin qualify, which the shared predicate already handles.

---

### Task 1: Backend — extend the env gate to the agent owner (TDD)

**Files:**
- Modify: `server/internal/handler/agent_env.go:53-92` (`authorizeAgentEnv`)
- Modify: `server/internal/handler/agent.go:917-928` (`canViewAgentSecrets` doc comment)
- Test: `server/internal/handler/agent_test.go` (append after `TestAgentEnv_TaskTokenActorSource`, ~line 465)

- [ ] **Step 1: Write the failing tests**

Append to `server/internal/handler/agent_test.go` (helpers `newRequestAs` and `withURLParam` already exist in `agent_access_test.go`, same package):

```go
// envOwnedAgentFixture creates a workspace-visible agent owned by a fresh
// plain-member user, plus a second unrelated plain member in the same
// workspace. Returns the agent id, the agent owner's user id, and the
// unrelated member's user id. Mirrors privateAgentTestFixture but with
// visibility='workspace' so loadAgentForUser passes for every member and
// these tests exercise the env authorization gate specifically.
func envOwnedAgentFixture(t *testing.T) (agentID, ownerID, otherID string) {
	t.Helper()
	ctx := context.Background()

	if err := testPool.QueryRow(ctx, `
		INSERT INTO "user" (name, email)
		VALUES ('Env Agent Owner', 'env-agent-owner@multica.test')
		RETURNING id
	`).Scan(&ownerID); err != nil {
		t.Fatalf("create owner user: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(),
			`DELETE FROM "user" WHERE email = 'env-agent-owner@multica.test'`)
	})

	if _, err := testPool.Exec(ctx, `
		INSERT INTO member (workspace_id, user_id, role)
		VALUES ($1, $2, 'member')
	`, testWorkspaceID, ownerID); err != nil {
		t.Fatalf("add owner as member: %v", err)
	}

	if err := testPool.QueryRow(ctx, `
		INSERT INTO "user" (name, email)
		VALUES ('Env Plain Member', 'env-plain-member@multica.test')
		RETURNING id
	`).Scan(&otherID); err != nil {
		t.Fatalf("create plain member user: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(),
			`DELETE FROM "user" WHERE email = 'env-plain-member@multica.test'`)
	})

	if _, err := testPool.Exec(ctx, `
		INSERT INTO member (workspace_id, user_id, role)
		VALUES ($1, $2, 'member')
	`, testWorkspaceID, otherID); err != nil {
		t.Fatalf("add plain member: %v", err)
	}

	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent (
			workspace_id, name, description, runtime_mode, runtime_config,
			runtime_id, visibility, max_concurrent_tasks, owner_id,
			instructions, custom_env, custom_args
		)
		VALUES ($1, 'env-owner-gate-agent', '', 'cloud', '{}'::jsonb,
		        $2, 'workspace', 1, $3, '', '{"KEY_ONE": "v1"}'::jsonb, '[]'::jsonb)
		RETURNING id
	`, testWorkspaceID, handlerTestRuntimeID(t), ownerID).Scan(&agentID); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, agentID)
	})

	return agentID, ownerID, otherID
}

// TestAgentEnv_AgentOwnerPlainMemberAllowed locks in the owner-access rule:
// the agent owner can reveal and edit env even when their workspace role is
// plain `member`. The audit rows must record the owner as the actor.
func TestAgentEnv_AgentOwnerPlainMemberAllowed(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	agentID, ownerID, _ := envOwnedAgentFixture(t)

	// Reveal.
	req := newRequestAs(ownerID, http.MethodGet, "/api/agents/"+agentID+"/env", nil)
	req = withURLParam(req, "id", agentID)
	w := httptest.NewRecorder()
	testHandler.GetAgentEnv(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GetAgentEnv as agent owner: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp AgentEnvResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.CustomEnv["KEY_ONE"] != "v1" {
		t.Errorf("expected KEY_ONE=v1, got %v", resp.CustomEnv)
	}

	// Audit row must record the agent owner as the actor.
	var actorID string
	if err := testPool.QueryRow(ctx, `
		SELECT actor_id::text FROM activity_log
		WHERE workspace_id = $1 AND action = 'agent_env_revealed'
		  AND details->>'agent_id' = $2
		ORDER BY created_at DESC LIMIT 1
	`, testWorkspaceID, agentID).Scan(&actorID); err != nil {
		t.Fatalf("no agent_env_revealed activity row found: %v", err)
	}
	if actorID != ownerID {
		t.Errorf("audit actor mismatch: got %s, want %s", actorID, ownerID)
	}

	// Edit.
	body := map[string]any{"custom_env": map[string]string{"KEY_ONE": "v1", "KEY_TWO": "v2"}}
	putReq := newRequestAs(ownerID, http.MethodPut, "/api/agents/"+agentID+"/env", body)
	putReq = withURLParam(putReq, "id", agentID)
	w = httptest.NewRecorder()
	testHandler.UpdateAgentEnv(w, putReq)
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateAgentEnv as agent owner: expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

// TestAgentEnv_NonOwnerPlainMemberForbidden keeps the old denial in place
// for members who neither own the agent nor hold a workspace owner/admin
// role.
func TestAgentEnv_NonOwnerPlainMemberForbidden(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID, _, otherID := envOwnedAgentFixture(t)

	cases := []struct {
		name   string
		method string
		fn     func(http.ResponseWriter, *http.Request)
		body   any
	}{
		{"reveal", http.MethodGet, testHandler.GetAgentEnv, nil},
		{"update", http.MethodPut, testHandler.UpdateAgentEnv, map[string]any{"custom_env": map[string]string{"K": "v"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := newRequestAs(otherID, tc.method, "/api/agents/"+agentID+"/env", tc.body)
			req = withURLParam(req, "id", agentID)
			w := httptest.NewRecorder()
			tc.fn(w, req)
			if w.Code != http.StatusForbidden {
				t.Fatalf("expected 403 for non-owner plain member, got %d: %s", w.Code, w.Body.String())
			}
		})
	}
}
```

- [ ] **Step 2: Run the new tests to verify they fail the right way**

Run: `cd server && go test ./internal/handler/ -run 'TestAgentEnv_AgentOwnerPlainMemberAllowed|TestAgentEnv_NonOwnerPlainMemberForbidden' -v`

Expected: `TestAgentEnv_AgentOwnerPlainMemberAllowed` FAILS with "expected 200, got 403" (owner is denied today); `TestAgentEnv_NonOwnerPlainMemberForbidden` PASSES (already-denied case). Requires the dev DB (`make db-up` + migrated); if the suite skips with "database not available", start the DB first.

- [ ] **Step 3: Implement the gate change**

In `server/internal/handler/agent_env.go`, replace the `authorizeAgentEnv` doc comment item 2 and the role check. The function becomes:

```go
// authorizeAgentEnv enforces the per-request auth contract for the env
// endpoints:
//
//  1. The actor MUST resolve to a member (human). Any request authored
//     by an agent token — even one whose backing member is a workspace
//     owner — is rejected. This is the key fix for the
//     impersonation/lateral-movement risk that motivated MUL-2600: an
//     agent running in the workspace cannot use its host's owner
//     credentials to reveal another agent's secrets.
//  2. The member must be a workspace owner/admin, or the agent's owner
//     (`agent.owner_id`) — the same rule as canManageAgent and
//     canViewAgentSecrets, so env management follows agent ownership.
//
// Returns the loaded agent and the authenticated member on success.
// All non-2xx branches write their own response and return ok=false.
func (h *Handler) authorizeAgentEnv(w http.ResponseWriter, r *http.Request) (db.Agent, db.Member, bool) {
	agentID := chi.URLParam(r, "id")
	agent, ok := h.loadAgentForUser(w, r, agentID)
	if !ok {
		return db.Agent{}, db.Member{}, false
	}

	workspaceID := uuidToString(agent.WorkspaceID)
	userID := requestUserID(r)

	// Reject agent actors before anything else. resolveActor returns
	// "agent" iff both X-Agent-ID and a valid X-Task-ID are present and
	// the task belongs to that agent — so this guard is precise and
	// cannot be tricked by a member-supplied header.
	actorType, _ := h.resolveActor(r, userID, workspaceID)
	if actorType == "agent" {
		writeError(w, http.StatusForbidden, "agents may not access env management endpoints")
		return db.Agent{}, db.Member{}, false
	}

	member, ok := h.requireWorkspaceRole(w, r, workspaceID, "agent not found", "owner", "admin", "member")
	if !ok {
		return db.Agent{}, db.Member{}, false
	}
	if !canViewAgentSecrets(agent, userID, member.Role) {
		writeError(w, http.StatusForbidden, "only the agent owner or a workspace owner/admin can manage agent env")
		return db.Agent{}, db.Member{}, false
	}

	return agent, member, true
}
```

(The diff: `requireWorkspaceRole(..., "owner", "admin")` → `requireWorkspaceRole(..., "owner", "admin", "member")` followed by the shared `canViewAgentSecrets` predicate, plus the comment/message updates. No other function in the file changes.)

- [ ] **Step 4: Update the `canViewAgentSecrets` doc comment**

In `server/internal/handler/agent.go:917-922`, the comment says the predicate "is shared only by the remaining mcp_config redaction path" — now it also gates the env endpoints. Replace the comment block with:

```go
// canViewAgentSecrets checks whether the requesting user is allowed to
// see or manage the agent's secret-bearing config. Only the agent owner
// or workspace owner/admin qualify. Shared by the mcp_config redaction
// path and the env-management endpoints (`authorizeAgentEnv`), so both
// secret surfaces follow the same ownership rule.
```

- [ ] **Step 5: Run the env test group to verify everything passes**

Run: `cd server && go test ./internal/handler/ -run 'TestAgentEnv|TestGetAgentEnv|TestUpdateAgentEnv|TestMergeAgentEnv' -v`

Expected: all PASS — including the pre-existing `TestGetAgentEnv_OwnerSucceedsAndAudits` (workspace owner path), `TestAgentEnv_AgentActorRejected`, and `TestAgentEnv_TaskTokenActorSource` (agent-actor denial must be unaffected).

- [ ] **Step 6: Commit**

```bash
git add server/internal/handler/agent_env.go server/internal/handler/agent.go server/internal/handler/agent_test.go
git commit -m "feat(agents): allow agent owner to reveal and edit env vars"
```

---

### Task 2: Backend documentation surfaces (CLI help, built-in skill, comments)

Per CLAUDE.md, changing product behavior that a built-in skill documents requires updating that skill's `SKILL.md` **and** its `references/*-source-map.md` in the same PR.

**Files:**
- Modify: `server/cmd/multica/cmd_agent.go:99,106,192-194`
- Modify: `server/internal/service/builtin_skills/multica-creating-agents/SKILL.md:23,124-131`
- Verify: `server/internal/service/builtin_skills/multica-creating-agents/references/creating-agents-source-map.md`
- Modify: `server/internal/handler/agent.go:85,1008`

- [ ] **Step 1: Update CLI help text in `cmd_agent.go`**

Line 99, replace:
```go
	Short: "Print an agent's custom_env as a JSON map (workspace owner/admin only; every call is recorded)",
```
with:
```go
	Short: "Print an agent's custom_env as a JSON map (agent owner or workspace owner/admin; every call is recorded)",
```

Line 106, replace:
```go
	Short: "Replace an agent's custom_env (workspace owner/admin only; values equal to **** preserve the existing entry)",
```
with:
```go
	Short: "Replace an agent's custom_env (agent owner or workspace owner/admin; values equal to **** preserve the existing entry)",
```

Lines 192-194 comment, replace `that path is owner/admin-only,` with `that path is restricted to the agent owner or workspace owner/admin,`.

- [ ] **Step 2: Update the built-in skill `SKILL.md`**

In `server/internal/service/builtin_skills/multica-creating-agents/SKILL.md`:

Line 23, replace:
```
multica agent env get <agent-id> --output json  # plaintext env (owner/admin only, agents denied)
```
with:
```
multica agent env get <agent-id> --output json  # plaintext env (agent owner or workspace owner/admin, agents denied)
```

Lines 124-127, replace:
```
- Reading plaintext values requires the dedicated `GET /api/agents/{id}/env`
  endpoint (`multica agent env get`). It is gated to workspace **owner/admin**
  members, and **agent actors are denied** regardless of the backing member's
  role — a running agent cannot read another agent's secrets.
```
with:
```
- Reading plaintext values requires the dedicated `GET /api/agents/{id}/env`
  endpoint (`multica agent env get`). It is gated to the **agent owner** plus
  workspace **owner/admin** members, and **agent actors are denied** regardless
  of the backing member's role — a running agent cannot read another agent's
  secrets.
```

Line 131 (in the "Writing values" bullet), replace `which is owner/admin-only` with `which is restricted to the agent owner or workspace owner/admin`.

- [ ] **Step 3: Verify the source map is still accurate**

Run: `grep -n "owner/admin\|env get\|env set" server/internal/service/builtin_skills/multica-creating-agents/references/creating-agents-source-map.md`

The `agent env get` / `agent env set` rows reference `cmd_agent.go` line numbers (874, 909) — Step 1 edited strings in place without adding/removing lines, so the numbers hold. If any row quotes the old "owner/admin only" wording, update that cell to "agent owner or workspace owner/admin". Expected: at most a wording touch-up, no line-number changes.

- [ ] **Step 4: Update stale comments in `agent.go`**

Line 85: replace `(owner/admin only,` with `(agent owner or workspace owner/admin,`.

Line 1008 comment: replace `that endpoint is owner/admin-only, denies` with `that endpoint is restricted to the agent owner or workspace owner/admin, denies`.

Then sweep for leftovers: `grep -rn "owner/admin only\|owner/admin-only" server/ --include="*.go"` — any remaining hit that describes the **env endpoints** gets the same rewording (hits about other features stay).

- [ ] **Step 5: Build to confirm nothing broke**

Run: `cd server && go build ./... && go vet ./cmd/multica/ ./internal/handler/`
Expected: clean exit.

- [ ] **Step 6: Commit**

```bash
git add server/cmd/multica/cmd_agent.go server/internal/service/builtin_skills/multica-creating-agents/ server/internal/handler/agent.go
git commit -m "docs(agents): document agent-owner access on env endpoints"
```

---

### Task 3: Frontend — EnvTab permission prop, i18n keys, component test (TDD)

**Files:**
- Modify: `packages/views/agents/components/tabs/env-tab.tsx:55-211`
- Modify: `packages/views/locales/en/agents.json` (~line 297, inside `tab_body.env`)
- Modify: `packages/views/locales/zh-Hans/agents.json` (~line 290)
- Modify: `packages/views/locales/ja/agents.json` (~line 287)
- Modify: `packages/views/locales/ko/agents.json` (~line 297)
- Create: `packages/views/agents/components/tabs/env-tab.test.tsx`

> Before writing the zh-Hans copy, read the Chinese voice guide at `apps/docs/content/docs/developers/conventions.zh.mdx` (CLAUDE.md requirement for translation edits). The strings below already follow the existing locale style (zh-Hans says 智能体所有者 / 工作区所有者 / 管理员, "解锁" for reveal — same as the neighbouring `redacted_hint` key).

- [ ] **Step 1: Add the i18n keys to all four locales**

In each `agents.json`, inside `tab_body.env`, insert a `no_permission_hint` key directly after `not_revealed_hint`, and **delete the orphaned `intro_readonly` key** from the same `env` block (it is referenced by no component — `grep -rn "intro_readonly" packages/ apps/ --include="*.tsx"` returns nothing — and it documents the old owner/admin-only rule). The parity test requires both edits in all four files.

`en/agents.json`:
```json
      "no_permission_hint": "Only the agent owner or a workspace owner/admin can reveal and edit these values.",
```

`zh-Hans/agents.json`:
```json
      "no_permission_hint": "只有智能体所有者或工作区所有者 / 管理员可以解锁并编辑这些值。",
```

`ja/agents.json`:
```json
      "no_permission_hint": "エージェントのオーナーまたはワークスペースのオーナー / 管理者のみが、これらの値を表示・編集できます。",
```

`ko/agents.json`:
```json
      "no_permission_hint": "에이전트 소유자 또는 워크스페이스 소유자/관리자만 이 값을 공개하고 수정할 수 있습니다.",
```

- [ ] **Step 2: Run the locale parity test to confirm the keys are consistent**

Run: `pnpm --filter @multica/views exec vitest run locales/parity.test.ts`
Expected: PASS.

- [ ] **Step 3: Write the failing component test**

Create `packages/views/agents/components/tabs/env-tab.test.tsx` (mock pattern mirrors `skills-tab.test.tsx` in the same directory):

```tsx
// @vitest-environment jsdom

import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import type { Agent } from "@multica/core/types";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../../locales/en/common.json";
import enAgents from "../../../locales/en/agents.json";

const TEST_RESOURCES = { en: { common: enCommon, agents: enAgents } };

const mockGetAgentEnv = vi.hoisted(() => vi.fn());

vi.mock("@multica/core/api", () => ({
  api: {
    getAgentEnv: (...args: unknown[]) => mockGetAgentEnv(...args),
    updateAgentEnv: vi.fn(),
  },
}));

vi.mock("sonner", () => ({
  toast: {
    error: vi.fn(),
    success: vi.fn(),
  },
}));

import { EnvTab } from "./env-tab";

const agent = {
  id: "agent-1",
  workspace_id: "ws-1",
  runtime_id: "runtime-1",
  name: "Agent",
  description: "",
  instructions: "",
  avatar_url: null,
  runtime_mode: "local",
  runtime_config: {},
  custom_args: [],
  visibility: "workspace",
  status: "idle",
  max_concurrent_tasks: 1,
  model: "",
  owner_id: "user-1",
  skills: [],
  custom_env_key_count: 1,
  created_at: "2026-06-12T00:00:00Z",
  updated_at: "2026-06-12T00:00:00Z",
  archived_at: null,
  archived_by: null,
} as Agent;

function renderEnvTab(canEdit: boolean) {
  return render(
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      <EnvTab agent={agent} canEdit={canEdit} />
    </I18nProvider>,
  );
}

describe("EnvTab", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("hides the reveal button and shows a permission hint when canEdit is false", () => {
    renderEnvTab(false);

    expect(
      screen.queryByRole("button", { name: /reveal & edit/i }),
    ).not.toBeInTheDocument();
    expect(
      screen.getByText(/only the agent owner or a workspace owner\/admin/i),
    ).toBeInTheDocument();
    // The configured-key count stays visible to everyone.
    expect(screen.getByText(/1 variable configured/i)).toBeInTheDocument();
  });

  it("shows the reveal button and fetches env on click when canEdit is true", async () => {
    mockGetAgentEnv.mockResolvedValue({
      agent_id: "agent-1",
      custom_env: { FOO: "bar" },
    });
    renderEnvTab(true);

    const button = screen.getByRole("button", { name: /reveal & edit/i });
    fireEvent.click(button);

    await waitFor(() => expect(mockGetAgentEnv).toHaveBeenCalledWith("agent-1"));
    expect(await screen.findByDisplayValue("FOO")).toBeInTheDocument();
  });
});
```

- [ ] **Step 4: Run the test to verify it fails**

Run: `pnpm --filter @multica/views exec vitest run agents/components/tabs/env-tab.test.tsx`
Expected: FAIL — the first test finds the reveal button (no `canEdit` gating exists yet); TypeScript may also flag the unknown `canEdit` prop.

- [ ] **Step 5: Implement the `canEdit` prop in EnvTab**

In `packages/views/agents/components/tabs/env-tab.tsx`, change the component signature (lines 55-67) to:

```tsx
export function EnvTab({
  agent,
  canEdit,
  onDirtyChange,
  onSaved,
}: {
  agent: Agent;
  /**
   * Mirrors the backend env gate (agent owner or workspace owner/admin —
   * `canEditAgent` in @multica/core/permissions). When false the tab is
   * read-only: configured-key count plus a permission hint, no Reveal
   * button — so members never hit the backend 403.
   */
  canEdit: boolean;
  onDirtyChange?: (dirty: boolean) => void;
  // Notifier so the parent page can refresh its agent cache after a
  // successful PUT — the parent owns the `Agent` object the rest of
  // the page reads (name, has_custom_env, etc.). Optional so call
  // sites without invalidation logic stay simple.
  onSaved?: () => void;
}) {
```

And change the pre-reveal block (lines 174-211) to:

```tsx
  if (revealed === null) {
    return (
      <div className="space-y-4">
        <div className="flex items-start justify-between gap-3">
          <div className="space-y-1">
            <p className="flex items-center gap-2 text-sm font-medium">
              <Lock className="h-3.5 w-3.5 text-muted-foreground" />
              {keyCount > 0
                ? t(($) => $.tab_body.env.not_revealed_title, {
                    count: keyCount,
                  })
                : t(($) => $.tab_body.env.not_revealed_empty)}
            </p>
            <p className="text-xs text-muted-foreground">
              {canEdit
                ? t(($) => $.tab_body.env.not_revealed_hint)
                : t(($) => $.tab_body.env.no_permission_hint)}
            </p>
          </div>
          {canEdit && (
            <Button
              type="button"
              variant="outline"
              size="sm"
              disabled={revealing}
              onClick={handleReveal}
              className="shrink-0"
            >
              {revealing ? (
                <Loader2 className="h-3.5 w-3.5 animate-spin" />
              ) : (
                <Eye className="h-3.5 w-3.5" />
              )}
              {revealing
                ? t(($) => $.tab_body.env.revealing)
                : t(($) => $.tab_body.env.reveal_action)}
            </Button>
          )}
        </div>
      </div>
    );
  }
```

(The editable state below is only reachable after a successful reveal, which requires the button — no change needed there.)

- [ ] **Step 6: Run the test to verify it passes**

Run: `pnpm --filter @multica/views exec vitest run agents/components/tabs/env-tab.test.tsx`
Expected: PASS (both tests).

- [ ] **Step 7: Commit**

```bash
git add packages/views/agents/components/tabs/env-tab.tsx packages/views/agents/components/tabs/env-tab.test.tsx packages/views/locales/
git commit -m "feat(agents): gate env reveal UI by agent-owner permission"
```

---

### Task 4: Thread the permission from the detail page into EnvTab

**Files:**
- Modify: `packages/views/agents/components/agent-overview-pane.tsx:74-86,244` (props + EnvTab call)
- Modify: `packages/views/agents/components/agent-detail-page.tsx:300-306` (pass prop)
- Modify: `packages/views/agents/components/agent-overview-pane.test.tsx:110-114` (new required prop)

- [ ] **Step 1: Add `canEdit` to AgentOverviewPane props**

In `agent-overview-pane.tsx`, extend the props interface (after the `onUpdate` entry at line 77):

```tsx
interface AgentOverviewPaneProps {
  agent: Agent;
  runtimes: AgentRuntime[];
  onUpdate: (id: string, data: Record<string, unknown>) => Promise<void>;
  /**
   * `canEditAgent` decision from the detail page (agent owner or workspace
   * owner/admin). Currently consumed by the Environment tab to hide the
   * audited Reveal flow from members who would get a 403.
   */
  canEdit: boolean;
  navIntent?: DetailTab | null;
  onNavIntentHandled?: () => void;
}
```

Add `canEdit` to the destructured parameters of `AgentOverviewPane` (line 111-117), and pass it where `EnvTab` is rendered (line 244):

```tsx
            <EnvTab
              agent={agent}
              canEdit={canEdit}
```
(keep the existing `onDirtyChange` / `onSaved` props unchanged).

- [ ] **Step 2: Pass the existing decision from the detail page**

In `agent-detail-page.tsx`, the page already computes `const { canEdit } = useAgentPermissions(agent, wsId)` (line 100). Add the prop at the `<AgentOverviewPane>` call (line 300):

```tsx
        <AgentOverviewPane
          agent={agent}
          runtimes={runtimes}
          onUpdate={handleUpdate}
          canEdit={canEdit.allowed}
          navIntent={tabNavIntent}
          onNavIntentHandled={() => setTabNavIntent(null)}
        />
```

- [ ] **Step 3: Fix the pane test for the new required prop**

In `agent-overview-pane.test.tsx` line 110, the single render helper passes `agent` / `runtimes` / `onUpdate` — add `canEdit={true}`:

```tsx
        <AgentOverviewPane
          agent={baseAgent}
          runtimes={runtimes}
          onUpdate={vi.fn().mockResolvedValue(undefined)}
          canEdit={true}
        />
```

- [ ] **Step 4: Typecheck and run the views package tests**

Run: `pnpm --filter @multica/views typecheck && pnpm --filter @multica/views test`
Expected: typecheck clean; full package suite PASS (per project rule: changed a shared component → run the whole package suite, sibling tests assert on these components).

- [ ] **Step 5: Commit**

```bash
git add packages/views/agents/components/agent-overview-pane.tsx packages/views/agents/components/agent-detail-page.tsx packages/views/agents/components/agent-overview-pane.test.tsx
git commit -m "feat(agents): thread canEdit permission into the env tab"
```

---

### Task 5: Product docs (4 languages)

**Files:**
- Modify: `apps/docs/content/docs/agents-create.mdx:67`
- Modify: `apps/docs/content/docs/agents-create.zh.mdx:67`
- Modify: `apps/docs/content/docs/agents-create.ja.mdx:67`
- Modify: `apps/docs/content/docs/agents-create.ko.mdx:67`

- [ ] **Step 1: Update the permission sentence in each language**

`agents-create.mdx` line 67, replace the phrase
`Reading values requires a workspace owner or admin to hit the dedicated, audited`
with
`Reading values requires the agent owner or a workspace owner or admin to hit the dedicated, audited`.

`agents-create.zh.mdx` line 67, replace
`读取真实值需要 workspace owner / admin 调用专用且会审计的`
with
`读取真实值需要智能体所有者或 workspace owner / admin 调用专用且会审计的`.

`agents-create.ja.mdx` line 67, replace
`実際の値を読み取るには、ワークスペースの owner または admin が、`
with
`実際の値を読み取るには、エージェントのオーナー、またはワークスペースの owner / admin が、`.

`agents-create.ko.mdx` line 67, replace
`실제 값을 읽으려면 워크스페이스 owner 또는 admin이`
with
`실제 값을 읽으려면 에이전트 소유자 또는 워크스페이스 owner / admin이`.

(Each file's surrounding sentences about agent-actor denial stay as-is — that behavior is unchanged.)

- [ ] **Step 2: Commit**

```bash
git add apps/docs/content/docs/agents-create.mdx apps/docs/content/docs/agents-create.zh.mdx apps/docs/content/docs/agents-create.ja.mdx apps/docs/content/docs/agents-create.ko.mdx
git commit -m "docs(agents): document agent-owner access to env reveal"
```

---

### Task 6: Final verification

Known environment caveat (from project memory): the full `make check` Go step has pre-existing environmental failures (config tests poisoned by `.env`, pg_cron flakes, agent-CLI tests) unrelated to this change — verify with isolated targeted runs instead.

- [ ] **Step 1: Targeted Go tests**

Run: `cd server && go test ./internal/handler/ -run 'TestAgentEnv|TestGetAgentEnv|TestUpdateAgentEnv|TestMergeAgentEnv|TestGetAgent_PrivateAgent|TestCanViewAgentSecrets|TestMemberAllowedForPrivateAgent' -v`
Expected: all PASS (skips are acceptable only if the test DB is down — in that case start it with `make db-up` and re-run).

- [ ] **Step 2: Full TS verification for the touched packages**

Run: `pnpm --filter @multica/views test && pnpm typecheck`
Expected: PASS / clean.

- [ ] **Step 3: Manual smoke (optional but recommended)**

With `make dev` running: log in as a plain member who owns an agent → Environment tab shows "Reveal & edit" and the reveal succeeds; log in as a different plain member → button is gone, permission hint shows; workspace owner → unchanged behavior.
