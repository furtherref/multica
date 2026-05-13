# Issue Mention Full Title Design

## Goal

Make both inserted issue mentions and the editor `@` issue suggestion panel show more of the issue title by default and expose the full title on hover, using one shared visibility rule across the two issue-reference entry points.

## Confirmed Scope

- Applies to markdown-rendered issue mentions globally, not just the issue detail body.
- Applies to the issue rows rendered in the editor `@` suggestion panel.
- Keeps the current inline chip interaction, issue-detail navigation behavior, and suggestion selection behavior.
- Improves readability without allowing long titles to destabilize paragraph layout or overflow the suggestion list.

## Current Context

Inserted markdown issue mentions currently resolve through a shared rendering path:

- `packages/views/common/markdown.tsx` injects the default issue mention renderer.
- `packages/views/issues/components/issue-mention-card.tsx` wraps the mention in navigation.
- `packages/views/issues/components/issue-chip.tsx` owns the visual structure and width behavior.

Today, `IssueChip` already renders status icon, identifier, and title, but the chip width is capped aggressively and the title is always truncated.

The editor suggestion list in `packages/views/editor/extensions/mention-suggestion.tsx` renders issue rows as `status + identifier + description(title)`, and that title is also always truncated.

Both surfaces have the same usability problem: users cannot reliably disambiguate long issue titles at the point where they reference an issue.

## Design

### Unified visibility rule

Use one rule for both surfaces:

- show more title text by default than today
- keep a hard upper bound and still truncate
- expose the full title through a hover tooltip when title data exists

This keeps the user experience consistent between "already inserted mention" and "choosing an issue to insert", without forcing the two components to share the same markup.

### Inserted mention behavior

Keep the existing `Markdown -> IssueMentionCard -> IssueChip` composition. Put the inserted-mention behavior change in `IssueChip` so every markdown surface inherits the same width and tooltip rules without per-page overrides.

`IssueMentionCard` should remain responsible only for linking to the issue detail page and hover affordance at the chip level.

Increase the chip's maximum width from the current compact setting to a larger but still bounded inline width. The chip should still be a single inline unit and still truncate when the title exceeds that larger limit.

This satisfies the confirmed direction:

- show more of the title directly in the body than today
- keep a hard cap so chat and comment paragraphs do not become visually unstable

### Suggestion panel behavior

Adjust the issue row in `mention-suggestion.tsx` so the title description gets more horizontal room inside the existing `w-72` popup layout while still truncating at a bounded width.

Issue rows should also expose the full title on hover. This should be attached to the title text region only, not to the entire row, so hover and focus behavior on the selectable button remains predictable.

### Tooltip behavior and `TooltipTrigger` convention

Add a tooltip for the title portion of a resolved issue mention and for the title portion of an issue suggestion row. Hovering the title text should show the full issue title in a tooltip.

The tooltip should be attached only when a real title exists. Fallback mentions that only show an identifier or shortened UUID should keep their current appearance and should not invent a tooltip.

The trigger area should be the title text region, not the whole chip or whole suggestion row. This avoids duplicate hover behavior on status icons, identifiers, and row selection affordances.

This change must follow the current `TooltipTrigger` convention introduced in commit `1f77351a`: prefer `TooltipTrigger render={...}` for these title-only triggers so the trigger element is explicit and does not create nested trigger structure around already interactive parents.

### Data flow

Do not add new API calls or new data types. Continue using the current `IssueChip` lookup flow:

1. Try the issue list query.
2. Fall back to the issue detail query when the mention is not present in the list data.

The suggestion panel should continue using the existing `label(identifier) + description(title) + status` data already produced by `issueToMention`.

The tooltip simply consumes the title string that already exists on the resolved issue entity or on the suggestion item description.

## Error and Fallback Handling

- If the issue cannot be resolved, keep the existing fallback label behavior.
- If the title is empty or missing, do not render a tooltip.
- The inserted mention change must not alter navigation behavior or loading fallback semantics.
- The suggestion panel change must not alter keyboard navigation, selection, or server-search merge behavior.

## Testing

Add focused coverage around both surfaces:

- `IssueChip`
  - resolved issue mention renders identifier and title with the widened chip width
  - resolved issue mention exposes the full issue title through tooltip content
  - resolved issue mention uses the title span as the tooltip trigger content via the `render` prop
  - unresolved issue mention still renders fallback text without tooltip-only assumptions
- `MentionList` / `MentionRow`
  - issue suggestion rows render identifier plus a less aggressively truncated title area
  - issue suggestion rows expose the full issue title through tooltip content
  - the tooltip trigger stays scoped to the title text instead of the whole button row

Existing consumers should not need broad new behavior tests unless they have mention-specific assertions that must be updated for the new tooltip wrapper.
