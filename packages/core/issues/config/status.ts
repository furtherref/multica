import type { IssueStatusCategory } from "../../types";

// These are keyed on CATEGORY, not on status key. A workspace can define any
// number of custom statuses, but every one of them belongs to exactly one of
// the 7 categories below — so board columns, the presentation config and the
// paginated fetch all keep a fixed shape. Resolve a status KEY to its category
// with the workspace catalog (`useIssueStatuses`) before indexing these.
// (MUL-6243)
//
// `archive` (fork status #39, migration 069) is the one key that is NOT a
// catalog category: the server refuses it as one, so no custom status can ever
// inherit it. It still resolves to a column and a label of its own, which is
// what keeps an archived issue out of `todo`. Hence the split below —
// STATUS_ORDER and STATUS_CONFIG cover it, ALL_STATUSES (the catalog
// categories: what the settings UI offers and what a fetch fans out over)
// does not.

/** Every key that renders as its own column, `archive` included. */
export const STATUS_ORDER: IssueStatusCategory[] = [
  "backlog",
  "todo",
  "in_progress",
  "in_review",
  "done",
  "blocked",
  "cancelled",
  "archive",
];

/** The 7 catalog categories, in display order. */
export const ALL_STATUSES: IssueStatusCategory[] = [
  "backlog",
  "todo",
  "in_progress",
  "in_review",
  "done",
  "blocked",
  "cancelled",
];

/** Statuses shown as board columns (excludes cancelled). */
export const BOARD_STATUSES: IssueStatusCategory[] = [
  "backlog",
  "todo",
  "in_progress",
  "in_review",
  "done",
  "blocked",
];

/**
 * Default-visible lifecycle statuses (MUL-4290 made `cancelled` one of them) —
 * the 7 catalog categories. `archive` stays opt-in: it is only ever shown via
 * an explicit status filter.
 */
export const DEFAULT_VISIBLE_STATUSES: IssueStatusCategory[] = [...BOARD_STATUSES, "cancelled"];

export const STATUS_CONFIG: Record<
  IssueStatusCategory,
  {
    label: string;
    iconColor: string;
    hoverBg: string;
    dividerColor: string;
    columnBg: string;
  }
> = {
  backlog: { label: "Backlog", iconColor: "text-muted-foreground", hoverBg: "hover:bg-accent", dividerColor: "bg-muted-foreground/40", columnBg: "bg-muted/40" },
  todo: { label: "Todo", iconColor: "text-muted-foreground", hoverBg: "hover:bg-accent", dividerColor: "bg-muted-foreground/40", columnBg: "bg-muted/40" },
  in_progress: { label: "In Progress", iconColor: "text-warning", hoverBg: "hover:bg-warning/10", dividerColor: "bg-warning", columnBg: "bg-warning/5" },
  in_review: { label: "In Review", iconColor: "text-success", hoverBg: "hover:bg-success/10", dividerColor: "bg-success", columnBg: "bg-success/5" },
  done: { label: "Done", iconColor: "text-info", hoverBg: "hover:bg-info/10", dividerColor: "bg-info", columnBg: "bg-info/5" },
  blocked: { label: "Blocked", iconColor: "text-destructive", hoverBg: "hover:bg-destructive/10", dividerColor: "bg-destructive", columnBg: "bg-destructive/5" },
  cancelled: { label: "Cancelled", iconColor: "text-muted-foreground", hoverBg: "hover:bg-accent", dividerColor: "bg-muted-foreground/40", columnBg: "bg-muted/40" },
  archive: { label: "Archive", iconColor: "text-muted-foreground", hoverBg: "hover:bg-accent", dividerColor: "bg-muted-foreground/40", columnBg: "bg-muted/40" },
};
