# Issue Mention Full Title Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make both inserted issue mentions and the editor `@` issue suggestion panel show more title text inline and show the full issue title on hover.

**Architecture:** Reuse the existing `Markdown -> IssueMentionCard -> IssueChip` pipeline for inserted mentions and keep that behavior centered in `IssueChip`. Update the editor suggestion row in `mention-suggestion.tsx` separately, but apply the same visibility rule and the same tooltip-trigger convention so both surfaces stay aligned without forcing shared markup.

**Tech Stack:** React, TypeScript, TanStack Query, existing `@multica/ui` tooltip primitives, Vitest, Testing Library.

---

## File Structure

- Modify: `packages/views/issues/components/issue-chip.tsx`
  - Keep inserted-mention behavior in the shared chip, widen the inline width cap, and add a title-only tooltip using the `TooltipTrigger render={...}` convention.
- Modify: `packages/views/editor/extensions/mention-suggestion.tsx`
  - Widen the title area in issue suggestion rows and add a title-only tooltip using the same trigger convention.
- Modify: `packages/views/issues/components/issue-chip.test.tsx`
  - Cover resolved and fallback mention rendering behavior and assert the tooltip trigger structure.
- Modify: `packages/views/editor/extensions/mention-suggestion.test.tsx`
  - Cover issue suggestion row title visibility and tooltip behavior.
- Verify existing consumers:
  - `packages/views/issues/components/issue-mention-card.tsx`
  - `packages/views/common/markdown.tsx`

## Task 1: Add Failing Tests for IssueChip

**Files:**
- Modify: `packages/views/issues/components/issue-chip.test.tsx`

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

- [ ] **Step 2: Write the failing test for the `TooltipTrigger render` convention**

```tsx
it("uses the title span as the tooltip trigger content for resolved issues", () => {
  render(<IssueChip issueId="issue-2" />, { wrapper: createWrapper() });

  expect(screen.getByTestId("tooltip-trigger")).toContainElement(
    screen.getAllByText("Tooltip trigger should reuse the title span")[0],
  );
});
```

Mock `TooltipTrigger` so it renders a stable `data-testid="tooltip-trigger"` wrapper around `render ?? children`. This guards against regressing back to a nested-trigger shape.

- [ ] **Step 3: Write the failing test for fallback rendering**

```tsx
it("renders fallback text without requiring tooltip data when the issue is unresolved", () => {
  render(<IssueChip issueId="missing-issue" fallbackLabel="MUL-404" />, {
    wrapper: createWrapper({ issues: [] }),
  });

  expect(screen.getByText("MUL-404")).toBeInTheDocument();
});
```

Model the wrapper and mocks after existing component tests such as `packages/views/issues/components/comment-card.test.tsx`: provide the required i18n/query context and use a simple tooltip mock when the real portal behavior would make the test brittle.

- [ ] **Step 4: Run the focused test file to verify failure**

Run: `pnpm turbo test --filter=@multica/views -- packages/views/issues/components/issue-chip.test.tsx`

Expected: FAIL because the tooltip structure does not exist yet and the test file may need new mocks or providers.

## Task 2: Add Failing Tests for Mention Suggestion Rows

**Files:**
- Modify: `packages/views/editor/extensions/mention-suggestion.test.tsx`

- [ ] **Step 1: Add a tooltip mock that exposes trigger and content**

```tsx
vi.mock("@multica/ui/components/ui/tooltip", () => ({
  Tooltip: ({ children }: { children: ReactNode }) => <>{children}</>,
  TooltipTrigger: ({ children, render }: { children?: ReactNode; render?: ReactNode }) => (
    <span data-testid="tooltip-trigger">{render ?? children}</span>
  ),
  TooltipContent: ({ children }: { children: ReactNode }) => (
    <div data-testid="tooltip-content">{children}</div>
  ),
}));
```

- [ ] **Step 2: Write the failing test for issue-row tooltip content**

```tsx
it("shows the full issue title in a tooltip for issue suggestion rows", async () => {
  searchIssuesMock.mockResolvedValue({
    issues: [
      {
        id: "i-1007",
        identifier: "MUL-1007",
        title: "A long issue title for the mention suggestion panel",
        status: "done",
      },
    ],
    total: 1,
  });

  render(<I18nWrapper><MentionList items={[]} query="long" command={vi.fn()} /></I18nWrapper>);

  await waitFor(() => {
    expect(screen.getByText("MUL-1007")).toBeInTheDocument();
  });

  expect(screen.getByTestId("tooltip-content")).toHaveTextContent(
    "A long issue title for the mention suggestion panel",
  );
});
```

- [ ] **Step 3: Write the failing test for title-only trigger scope**

```tsx
it("keeps the tooltip trigger scoped to the issue title text", async () => {
  searchIssuesMock.mockResolvedValue({
    issues: [
      {
        id: "i-1008",
        identifier: "MUL-1008",
        title: "Title-only tooltip trigger",
        status: "todo",
      },
    ],
    total: 1,
  });

  render(<I18nWrapper><MentionList items={[]} query="trigger" command={vi.fn()} /></I18nWrapper>);

  await waitFor(() => {
    expect(screen.getByText("MUL-1008")).toBeInTheDocument();
  });

  expect(screen.getByTestId("tooltip-trigger")).toContainElement(
    screen.getByText("Title-only tooltip trigger"),
  );
});
```

- [ ] **Step 4: Run the focused mention-suggestion test file to verify failure**

Run: `pnpm turbo test --filter=@multica/views -- packages/views/editor/extensions/mention-suggestion.test.tsx`

Expected: FAIL because issue rows do not yet render tooltip primitives.

## Task 3: Implement the Shared Inserted-Mention Behavior

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
        <TooltipTrigger
          render={<span className="text-foreground truncate">{issue.title}</span>}
        />
        <TooltipContent>{issue.title}</TooltipContent>
      </Tooltip>
```

Use this only in the resolved issue branch. Keep the fallback branch unchanged. Do not switch back to a wrapped-children trigger here; commit `1f77351a` established the `render` prop pattern for this case to avoid nested tooltip trigger structure.

- [ ] **Step 3: Preserve identifier and navigation semantics**

```tsx
      <span className="font-medium text-muted-foreground shrink-0">
        {issue.identifier}
      </span>
```

Do not move link ownership into `IssueChip`; `IssueMentionCard` remains the clickable wrapper.

- [ ] **Step 4: Run the focused test file**

Run: `pnpm turbo test --filter=@multica/views -- packages/views/issues/components/issue-chip.test.tsx`

Expected: PASS

## Task 4: Implement the Suggestion Panel Behavior

**Files:**
- Modify: `packages/views/editor/extensions/mention-suggestion.tsx`

- [ ] **Step 1: Import the shared tooltip primitives**

```tsx
import { Tooltip, TooltipTrigger, TooltipContent } from "@multica/ui/components/ui/tooltip";
```

- [ ] **Step 2: Widen the issue-row title area without changing row selection structure**

```tsx
        <span className="shrink-0 text-muted-foreground">{item.label}</span>
        {item.description && (
          <Tooltip>
            <TooltipTrigger
              render={
                <span
                  className={`min-w-0 flex-1 truncate text-muted-foreground ${
                    isClosed ? "line-through" : ""
                  }`}
                >
                  {item.description}
                </span>
              }
            />
            <TooltipContent>{item.description}</TooltipContent>
          </Tooltip>
        )}
```

Keep the outer element as the existing `<button>` so keyboard navigation and click selection remain unchanged.

- [ ] **Step 3: Run the focused mention-suggestion test file**

Run: `pnpm turbo test --filter=@multica/views -- packages/views/editor/extensions/mention-suggestion.test.tsx`

Expected: PASS

## Task 5: Verify Shared Consumers Still Behave

**Files:**
- Verify: `packages/views/issues/components/issue-mention-card.tsx`
- Verify: `packages/views/common/markdown.tsx`

- [ ] **Step 1: Confirm no consumer code changes are needed**

Check that `IssueMentionCard` still wraps `IssueChip` in `AppLink` and that `Markdown` still resolves issue mentions through `IssueMentionCard`.

- [ ] **Step 2: Run a targeted mention-related test pass**

Run: `pnpm turbo test --filter=@multica/views -- packages/views/issues/components/comment-card.test.tsx packages/views/chat/components/context-anchor.test.ts packages/views/editor/extensions/mention-extension.test.ts packages/views/editor/extensions/mention-suggestion.test.tsx`

Expected: PASS

## Task 6: Run Final Verification and Commit

**Files:**
- Modify: `packages/views/issues/components/issue-chip.tsx`
- Modify: `packages/views/editor/extensions/mention-suggestion.tsx`
- Test: `packages/views/issues/components/issue-chip.test.tsx`
- Test: `packages/views/editor/extensions/mention-suggestion.test.tsx`

- [ ] **Step 1: Run the project checks needed for this change**

Run: `pnpm turbo test --filter=@multica/views -- packages/views/issues/components/issue-chip.test.tsx packages/views/editor/extensions/mention-suggestion.test.tsx packages/views/issues/components/comment-card.test.tsx packages/views/chat/components/context-anchor.test.ts packages/views/editor/extensions/mention-extension.test.ts`

Expected: PASS

- [ ] **Step 2: Inspect the diff**

Run: `git diff -- packages/views/issues/components/issue-chip.tsx packages/views/issues/components/issue-chip.test.tsx packages/views/editor/extensions/mention-suggestion.tsx packages/views/editor/extensions/mention-suggestion.test.tsx`

Expected: Only the inserted-mention width/tooltip behavior, the suggestion-row width/tooltip behavior, and focused tests.

- [ ] **Step 3: Commit the change**

```bash
git add packages/views/issues/components/issue-chip.tsx packages/views/issues/components/issue-chip.test.tsx packages/views/editor/extensions/mention-suggestion.tsx packages/views/editor/extensions/mention-suggestion.test.tsx
git commit -m "feat: improve issue mention title visibility"
```
