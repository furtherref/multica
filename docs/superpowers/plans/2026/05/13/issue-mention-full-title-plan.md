# Issue Mention Full Title Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make markdown issue mentions show more title text inline and show the full issue title on hover across all shared markdown surfaces.

**Architecture:** Reuse the existing `Markdown -> IssueMentionCard -> IssueChip` pipeline and keep the implementation centered in `IssueChip`. Adjust the chip width budget there, wrap only the title span with the shared tooltip primitives, and verify behavior with focused component tests rather than per-page duplication.

**Tech Stack:** React, TypeScript, TanStack Query, existing `@multica/ui` tooltip primitives, Vitest, Testing Library.

---

## File Structure

- Modify: `packages/views/issues/components/issue-chip.tsx`
  - Widen the inline chip width cap and add a title-only tooltip for resolved issues.
- Create: `packages/views/issues/components/issue-chip.test.tsx`
  - Cover resolved and fallback mention rendering behavior.
- Verify existing consumers:
  - `packages/views/issues/components/issue-mention-card.tsx`
  - `packages/views/common/markdown.tsx`

## Task 1: Add Failing Tests for IssueChip

**Files:**
- Create: `packages/views/issues/components/issue-chip.test.tsx`

- [ ] **Step 1: Write the failing test for the resolved issue tooltip**

```tsx
it("shows the full issue title in a tooltip for resolved issues", async () => {
  render(<IssueChip issueId="issue-1" />, { wrapper: createWrapper() });

  const title = await screen.findByText("A very long issue title that is truncated inline");
  await userEvent.hover(title);

  expect(
    await screen.findByText("A very long issue title that is truncated inline"),
  ).toBeInTheDocument();
});
```

- [ ] **Step 2: Write the failing test for fallback rendering**

```tsx
it("renders fallback text without requiring tooltip data when the issue is unresolved", () => {
  render(<IssueChip issueId="missing-issue" fallbackLabel="MUL-404" />, {
    wrapper: createWrapper({ issues: [] }),
  });

  expect(screen.getByText("MUL-404")).toBeInTheDocument();
});
```

Model the wrapper and mocks after existing component tests such as `packages/views/issues/components/comment-card.test.tsx`: provide the required i18n/query context and use a simple tooltip mock when the real portal behavior would make the test brittle.

- [ ] **Step 3: Run the focused test file to verify failure**

Run: `pnpm vitest run packages/views/issues/components/issue-chip.test.tsx`

Expected: FAIL because the tooltip structure does not exist yet and the test file may need new mocks or providers.

## Task 2: Implement the Shared IssueChip Behavior

**Files:**
- Modify: `packages/views/issues/components/issue-chip.tsx`

- [ ] **Step 1: Add tooltip primitives and widen the chip width budget**

```tsx
import { Tooltip, TooltipTrigger, TooltipContent } from "@multica/ui/components/ui/tooltip";

const BASE_CLASS =
  "issue-mention inline-flex items-center gap-1.5 rounded-md border mx-0.5 px-2 py-0.5 text-xs max-w-96";
```

- [ ] **Step 2: Wrap the resolved title span with a title-only tooltip**

```tsx
      <Tooltip>
        <TooltipTrigger>
          <span className="text-foreground truncate">{issue.title}</span>
        </TooltipTrigger>
        <TooltipContent>{issue.title}</TooltipContent>
      </Tooltip>
```

Use this only in the resolved issue branch. Keep the fallback branch unchanged.

- [ ] **Step 3: Preserve identifier and navigation semantics**

```tsx
      <span className="font-medium text-muted-foreground shrink-0">
        {issue.identifier}
      </span>
```

Do not move link ownership into `IssueChip`; `IssueMentionCard` remains the clickable wrapper.

- [ ] **Step 4: Run the focused test file**

Run: `pnpm vitest run packages/views/issues/components/issue-chip.test.tsx`

Expected: PASS

## Task 3: Verify Shared Consumers Still Behave

**Files:**
- Verify: `packages/views/issues/components/issue-mention-card.tsx`
- Verify: `packages/views/common/markdown.tsx`

- [ ] **Step 1: Confirm no consumer code changes are needed**

Check that `IssueMentionCard` still wraps `IssueChip` in `AppLink` and that `Markdown` still resolves issue mentions through `IssueMentionCard`.

- [ ] **Step 2: Run a targeted mention-related test pass**

Run: `pnpm vitest run packages/views/issues/components/comment-card.test.tsx packages/views/chat/components/context-anchor.test.ts packages/views/editor/extensions/mention-extension.test.ts`

Expected: PASS

## Task 4: Run Final Verification and Commit

**Files:**
- Modify: `packages/views/issues/components/issue-chip.tsx`
- Test: `packages/views/issues/components/issue-chip.test.tsx`

- [ ] **Step 1: Run the project checks needed for this change**

Run: `pnpm vitest run packages/views/issues/components/issue-chip.test.tsx packages/views/issues/components/comment-card.test.tsx packages/views/chat/components/context-anchor.test.ts packages/views/editor/extensions/mention-extension.test.ts`

Expected: PASS

- [ ] **Step 2: Inspect the diff**

Run: `git diff -- packages/views/issues/components/issue-chip.tsx packages/views/issues/components/issue-chip.test.tsx`

Expected: Only the chip width and tooltip behavior, plus focused tests.

- [ ] **Step 3: Commit the change**

```bash
git add packages/views/issues/components/issue-chip.tsx packages/views/issues/components/issue-chip.test.tsx
git commit -m "feat: improve issue mention title visibility"
```
