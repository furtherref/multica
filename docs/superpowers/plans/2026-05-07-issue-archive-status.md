# Issue Archive Status Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an `archive` issue status that users can select, while keeping archived issues out of the kanban board.

**Architecture:** The shared issue status union and status config are the source of truth for frontend pickers and board columns. The CLI maintains its own issue status allowlist, so it must be updated separately. Backend done/cancelled semantics stay unchanged.

**Tech Stack:** TypeScript, React view packages, Vitest, Go Cobra CLI tests.

---

## File Structure

- Modify `packages/core/types/issue.ts`: add `archive` to the `IssueStatus` union.
- Modify `packages/core/issues/config/status.ts`: add `archive` to `STATUS_ORDER`, `ALL_STATUSES`, and `STATUS_CONFIG`; keep it out of `BOARD_STATUSES`.
- Create `packages/core/issues/config/status.test.ts`: assert shared status lists include `archive` and board columns exclude it.
- Modify `packages/views/locales/en/issues.json`: add the English `archive` status label.
- Modify `packages/views/locales/zh-Hans/issues.json`: add the Simplified Chinese `archive` status label.
- Modify `server/cmd/multica/cmd_issue.go`: add `archive` to `validIssueStatuses`.
- Modify `server/cmd/multica/cmd_issue_test.go`: update the existing status allowlist test to expect `archive`.

### Task 1: Core Status Model

**Files:**
- Create: `packages/core/issues/config/status.test.ts`
- Modify: `packages/core/types/issue.ts`
- Modify: `packages/core/issues/config/status.ts`

- [ ] **Step 1: Write the failing test**

Create `packages/core/issues/config/status.test.ts`:

```ts
import { describe, expect, it } from "vitest";
import { ALL_STATUSES, BOARD_STATUSES, STATUS_CONFIG, STATUS_ORDER } from "./status";

describe("issue status config", () => {
  it("includes archive as a selectable issue status", () => {
    expect(STATUS_ORDER).toContain("archive");
    expect(ALL_STATUSES).toContain("archive");
    expect(STATUS_CONFIG.archive.label).toBe("Archive");
  });

  it("excludes archive from board columns", () => {
    expect(BOARD_STATUSES).not.toContain("archive");
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `pnpm --filter @multica/core test -- issues/config/status.test.ts`

Expected: FAIL because `STATUS_CONFIG.archive` is undefined and `archive` is absent from status arrays.

- [ ] **Step 3: Write minimal implementation**

In `packages/core/types/issue.ts`, change the union to:

```ts
export type IssueStatus =
  | "backlog"
  | "todo"
  | "in_progress"
  | "in_review"
  | "done"
  | "blocked"
  | "cancelled"
  | "archive";
```

In `packages/core/issues/config/status.ts`, add `archive` to `STATUS_ORDER` and `ALL_STATUSES` after `cancelled`, keep `BOARD_STATUSES` unchanged, and add:

```ts
archive: { label: "Archive", iconColor: "text-muted-foreground", hoverBg: "hover:bg-accent", dividerColor: "bg-muted-foreground/40", columnBg: "bg-muted/40" },
```

- [ ] **Step 4: Run test to verify it passes**

Run: `pnpm --filter @multica/core test -- issues/config/status.test.ts`

Expected: PASS.

### Task 2: Localized Status Labels

**Files:**
- Modify: `packages/views/locales/en/issues.json`
- Modify: `packages/views/locales/zh-Hans/issues.json`

- [ ] **Step 1: Update labels**

In `packages/views/locales/en/issues.json`, add:

```json
"archive": "Archive"
```

inside the top-level `status` object after `cancelled`.

In `packages/views/locales/zh-Hans/issues.json`, add:

```json
"archive": "已归档"
```

inside the top-level `status` object after `cancelled`.

- [ ] **Step 2: Verify locale JSON parses**

Run: `node -e "JSON.parse(require('fs').readFileSync('packages/views/locales/en/issues.json','utf8')); JSON.parse(require('fs').readFileSync('packages/views/locales/zh-Hans/issues.json','utf8'))"`

Expected: command exits 0 with no output.

### Task 3: CLI Status Allowlist

**Files:**
- Modify: `server/cmd/multica/cmd_issue_test.go`
- Modify: `server/cmd/multica/cmd_issue.go`

- [ ] **Step 1: Write the failing test update**

In `server/cmd/multica/cmd_issue_test.go`, update `TestValidIssueStatuses` expected map:

```go
expected := map[string]bool{
	"backlog":     true,
	"todo":        true,
	"in_progress": true,
	"in_review":   true,
	"done":        true,
	"blocked":     true,
	"cancelled":   true,
	"archive":     true,
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/multica -run TestValidIssueStatuses -count=1`

from the `server` directory.

Expected: FAIL because `validIssueStatuses` has 7 entries instead of 8.

- [ ] **Step 3: Write minimal implementation**

In `server/cmd/multica/cmd_issue.go`, update:

```go
var validIssueStatuses = []string{
	"backlog", "todo", "in_progress", "in_review", "done", "blocked", "cancelled", "archive",
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./cmd/multica -run TestValidIssueStatuses -count=1`

from the `server` directory.

Expected: PASS.

### Task 4: Final Verification and Commit

**Files:**
- All files changed in Tasks 1-3.

- [ ] **Step 1: Run focused tests**

Run:

```bash
pnpm --filter @multica/core test -- issues/config/status.test.ts
go test ./cmd/multica -run TestValidIssueStatuses -count=1
```

Run the Go command from `server`.

Expected: both commands pass.

- [ ] **Step 2: Run type checks for touched frontend packages**

Run:

```bash
pnpm --filter @multica/core typecheck
pnpm --filter @multica/views typecheck
```

Expected: both commands pass.

- [ ] **Step 3: Review changed files**

Run: `git diff --check`

Expected: no whitespace errors.

Run: `git diff --stat`

Expected: only the planned files changed.

- [ ] **Step 4: Commit**

Run:

```bash
git add packages/core/types/issue.ts packages/core/issues/config/status.ts packages/core/issues/config/status.test.ts packages/views/locales/en/issues.json packages/views/locales/zh-Hans/issues.json server/cmd/multica/cmd_issue.go server/cmd/multica/cmd_issue_test.go
git commit -m "feat: add archive issue status"
```

Expected: commit succeeds.
