# Runtime Daily Cost Column Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a final Cost column to the Runtime daily breakdown table using the existing canonical client-side pricing formula.

**Architecture:** Keep the change in the shared `packages/views` Runtime usage component. The existing `RuntimeUsage` rows and `estimateCost` helper already carry and calculate every required cost source, so the implementation only extends presentation and locale resources.

**Tech Stack:** React, TypeScript, Tailwind CSS grid utilities, i18next JSON resources, Vitest, Testing Library.

**Spec:** `docs/superpowers/specs/2026-09-03-runtime-daily-cost-column-design.md`

## Global Constraints

- Work directly in the current checkout on `codex/runtime-daily-cost-column`; do not create a worktree.
- Do not use subagents.
- Reuse `estimateCost` and `formatUsd`; do not add another pricing formula or backend cost field.
- Keep Web and Desktop behavior shared through `packages/views`.

---

### Task 1: Daily breakdown Cost column

**Files:**
- Modify: `packages/views/runtimes/components/usage-section.test.tsx`
- Modify: `packages/views/runtimes/components/usage-section.tsx:933-978`
- Modify: `packages/views/locales/en/runtimes.json`
- Modify: `packages/views/locales/zh-Hans/runtimes.json`
- Modify: `packages/views/locales/ja/runtimes.json`
- Modify: `packages/views/locales/ko/runtimes.json`

**Interfaces:**
- Consumes: `estimateCost(usage: Priceable): number`, `formatUsd(n: number): string`, and each `RuntimeUsage` row already passed to `DailyBreakdownTable`.
- Produces: a translated `usage.table_cost` header and a formatted per-row Cost cell at the end of the table.

- [ ] **Step 1: Write the failing component test**

Add a test that provides a current-day usage row with `$0.35` of provider-reported cost, renders `UsageSection`, clicks the `Daily breakdown table` button, and asserts the Cost header and formatted row value within the folded section:

```tsx
it("shows the calculated cost as the final daily breakdown column", () => {
  usageOverride.rows = [{
    runtime_id: "r-1",
    date: new Date().toISOString().slice(0, 10),
    provider: "copilot",
    model: "gpt-5.5",
    input_tokens: 34_300,
    output_tokens: 3_000,
    cache_read_tokens: 175_700,
    cache_write_tokens: 0,
    cost_usd_ticks: 3_500_000_000,
    uncosted_input_tokens: 0,
    uncosted_output_tokens: 0,
    uncosted_cache_read_tokens: 0,
    uncosted_cache_write_tokens: 0,
  }];
  render(<UsageSection runtime={RUNTIME} />, { wrapper: Wrapper });

  const toggle = screen.getByRole("button", { name: "Daily breakdown table" });
  fireEvent.click(toggle);
  const breakdown = within(toggle.parentElement!);

  expect(breakdown.getByText("Cost")).toBeInTheDocument();
  expect(breakdown.getByText("$0.35")).toBeInTheDocument();
});
```

- [ ] **Step 2: Run the focused test and confirm RED**

Run:

```bash
pnpm --filter @multica/views test -- usage-section.test.tsx
```

Expected: FAIL because the daily breakdown has no `Cost` header or cost cell.

- [ ] **Step 3: Implement the minimal shared-view change**

Append an 80px grid track to both table row templates, add the translated header after Cache write, and add this right-aligned cell after `cache_write_tokens`:

```tsx
<div className="text-right font-medium tabular-nums">
  {formatUsd(estimateCost(row))}
</div>
```

Add `"table_cost": "Cost"`, `"table_cost": "费用"`, and the corresponding Japanese and Korean translations beside the existing table labels.

- [ ] **Step 4: Run focused and package verification**

Run:

```bash
pnpm --filter @multica/views test -- usage-section.test.tsx
pnpm --filter @multica/views typecheck
pnpm --filter @multica/views lint
git diff --check
```

Expected: every command exits 0.

- [ ] **Step 5: Review and commit**

Inspect the scoped diff, confirm no unrelated files or secrets are included, then commit:

```bash
git add docs/superpowers/specs/2026-09-03-runtime-daily-cost-column-design.md docs/superpowers/plans/2026-09-03-runtime-daily-cost-column.md packages/views/runtimes/components/usage-section.test.tsx packages/views/runtimes/components/usage-section.tsx packages/views/locales/en/runtimes.json packages/views/locales/zh-Hans/runtimes.json packages/views/locales/ja/runtimes.json packages/views/locales/ko/runtimes.json
git commit -m "feat(runtimes): show cost in daily usage breakdown"
```
