"use client";

import { useMemo } from "react";
import { ALL_STATUSES } from "@multica/core/issues/config";
import { useIssueStatuses } from "@multica/core/issue-statuses/hooks";
import { issueStatusColor } from "@multica/core/issue-statuses/queries";
import type { IssueStatus, IssueStatusCategory } from "@multica/core/types";
import { useStatusLabel } from "./status-label";

export interface StatusOption {
  key: IssueStatus;
  /** The category this status behaves as — drives its icon and hover color. */
  category: IssueStatusCategory;
  label: string;
  /** `#rrggbb` for a custom status; null for a built-in, which keeps its token color. */
  color: string | null;
}

/**
 * The statuses a user can pick or filter by, as one flat list in canonical
 * category order (MUL-6243, MUL-6399).
 *
 * Category is carried per option rather than expressed as a heading: it is the
 * behavior a status inherits, which the icon and hover color already say, and
 * a heading per category turned a 7-row list into 14 rows of half whitespace.
 *
 * Shared by the status picker and the status filter so the two can never drift
 * — a status offered in one and missing from the other is exactly how an issue
 * becomes unfindable.
 *
 * Archived statuses are excluded: archiving retires a status from future
 * assignment. Issues already on one keep it, and `useStatusLabel` still names
 * it, because the catalog query keeps archived rows.
 *
 * `archive` (fork status #39) is appended by hand. It is not a catalog
 * category, so the loop below cannot produce it — but it has to stay on this
 * list, because this list is the ONLY way to archive an issue and the only way
 * to filter for archived work. It sorts last, where STATUS_ORDER puts it.
 */
export function useStatusOptions(wsId: string): StatusOption[] {
  const { activeStatuses } = useIssueStatuses(wsId);
  const labelOf = useStatusLabel(wsId);

  return useMemo(
    () =>
      ALL_STATUSES.flatMap((category) => {
        const entries = activeStatuses.filter((e) => e.category === category);
        // No catalog row for this category: the fetch is still in flight, or
        // this workspace predates the seed. Offer the built-in, whose key IS
        // the category, so a lifecycle step is never missing.
        if (entries.length === 0) {
          return [{ key: category as IssueStatus, category, label: labelOf(category), color: null }];
        }
        return entries.map((e) => ({
          key: e.key as IssueStatus,
          category,
          label: labelOf(e.key),
          color: issueStatusColor(e),
        }));
      }).concat([
        {
          key: "archive" as IssueStatus,
          category: "archive",
          label: labelOf("archive"),
          color: null,
        },
      ]),
    [activeStatuses, labelOf],
  );
}
