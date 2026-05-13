# Issue Mention Full Title Design

## Goal

Make issue mentions rendered from markdown show more of the issue title inline and expose the full title on hover, with one shared behavior across every markdown surface that uses issue mentions.

## Confirmed Scope

- Applies to markdown-rendered issue mentions globally, not just the issue detail body.
- Keeps the current inline chip interaction and issue-detail navigation behavior.
- Improves readability without allowing long titles to destabilize paragraph layout.

## Current Context

Markdown issue mentions currently resolve through a shared rendering path:

- `packages/views/common/markdown.tsx` injects the default issue mention renderer.
- `packages/views/issues/components/issue-mention-card.tsx` wraps the mention in navigation.
- `packages/views/issues/components/issue-chip.tsx` owns the visual structure and width behavior.

Today, `IssueChip` already renders status icon, identifier, and title, but the chip width is capped aggressively and the title is always truncated. There is no tooltip for the full title.

## Design

### Shared entry point

Keep the existing `Markdown -> IssueMentionCard -> IssueChip` composition. Put the behavior change in `IssueChip` so every markdown surface inherits the same width and tooltip rules without per-page overrides.

`IssueMentionCard` should remain responsible only for linking to the issue detail page and hover affordance at the chip level.

### Inline width behavior

Increase the chip's maximum width from the current compact setting to a larger but still bounded inline width. The chip should still be a single inline unit and still truncate when the title exceeds that larger limit.

This satisfies the confirmed direction:

- show more of the title directly in the body than today
- keep a hard cap so chat and comment paragraphs do not become visually unstable

### Tooltip behavior

Add a tooltip for the title portion of a resolved issue mention. Hovering the title text should show the full issue title in a tooltip.

The tooltip should be attached only when the issue has been resolved successfully and a real title exists. Fallback mentions that only show an identifier or shortened UUID should keep their current appearance and should not invent a tooltip.

The trigger area should be the title text region inside the chip, not the whole chip. This avoids duplicate hover behavior on the status icon and identifier while keeping the click target unchanged.

### Data flow

Do not add new API calls or new data types. Continue using the current `IssueChip` lookup flow:

1. Try the issue list query.
2. Fall back to the issue detail query when the mention is not present in the list data.

The tooltip simply consumes the title that already exists on the resolved issue entity.

## Error and Fallback Handling

- If the issue cannot be resolved, keep the existing fallback label behavior.
- If the title is empty or missing, do not render a tooltip.
- The change must not alter navigation behavior or loading fallback semantics.

## Testing

Add focused component coverage around `IssueChip`:

- resolved issue mention renders identifier and title with the widened chip width
- resolved issue mention exposes the full title through tooltip content
- unresolved issue mention still renders fallback text without tooltip-only assumptions

Existing consumers should not need separate behavior tests unless they have mention-specific assertions that must be updated for the new tooltip wrapper.
