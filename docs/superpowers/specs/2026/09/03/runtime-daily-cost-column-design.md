# Runtime Daily Cost Column Design

## Goal

Add a final Cost column to the Runtime statistics daily breakdown table so each displayed date/provider/model usage row shows the same cost used by the page KPI and cost charts.

## Design

The change stays in the shared Runtime usage view, so Web and Desktop receive identical behavior. `DailyBreakdownTable` will append a right-aligned fixed-width column and render `formatUsd(estimateCost(row))` for each existing `RuntimeUsage` row.

No API, SQL, schema, or database change is needed. Runtime usage rows already contain provider/model identity, token buckets, provider-reported `cost_usd_ticks`, and the uncosted-token split required by `estimateCost`. Reusing that helper preserves authoritative provider costs, maintained pricing, and local custom pricing without creating a second pricing formula.

The column represents the current row's date/provider/model cost, not a date-wide subtotal. Missing telemetry remains represented by the existing page-level lower-bound warning because a missing run cannot be assigned to a model row.

## Presentation

- Append the column after Cache write.
- Use the existing two-decimal USD formatter for consistency with other Runtime cost displays.
- Add `usage.table_cost` in English, Simplified Chinese, Japanese, and Korean locale resources.
- Preserve the table's current ordering, vertical scrolling, and model truncation.

## Verification

Add a shared-view component test that expands the daily breakdown table and proves the Cost header and a calculated row value are rendered. Run the focused Vitest suite, package typecheck and lint, and inspect the final diff before delivery.
