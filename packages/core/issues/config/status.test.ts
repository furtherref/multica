// @vitest-environment node
import { describe, expect, it } from "vitest";
import {
  ALL_STATUSES,
  BUILT_IN_STATUS_CATEGORY,
  BUILT_IN_STATUS_LABEL,
  BUILT_IN_STATUS_ORDER,
  STATUS_ORDER,
  normalizeIssueStatusCategory,
} from "./status";
import { statusColumnKeys, visibleStatusKeys } from "../status-category";

const emptyCatalog = {
  statuses: [],
  entryOf: () => undefined,
  isLoaded: true,
  isPending: false,
  isError: false,
};

describe("issue status config", () => {
  // `archive` (fork status #39) has to render — as a column, an icon and a
  // label — without being offerable as a CATALOG category. The server refuses
  // it as one, so a custom status can never inherit its retired-work guards,
  // and the settings UI must never list it.
  it("treats archive as a built-in key in the closed lifecycle, never a category", () => {
    expect(STATUS_ORDER).not.toContain("archive");
    expect(ALL_STATUSES).not.toContain("archive");
    expect(BUILT_IN_STATUS_ORDER.at(-1)).toBe("archive");
    expect(BUILT_IN_STATUS_CATEGORY.archive).toBe("closed");
    expect(BUILT_IN_STATUS_LABEL.archive).toBe("Archive");
    // The server keeps `archive` raw in status_category for installed clients.
    expect(normalizeIssueStatusCategory("archive")).toBe("closed");
  });

  it("keeps archive out of the default columns and hidden-column toggles", () => {
    expect(statusColumnKeys(emptyCatalog)).not.toContain("archive");
    expect(visibleStatusKeys([], [], emptyCatalog)).not.toContain("archive");
  });

  it("shows the archive column only through an explicit status filter", () => {
    expect(statusColumnKeys(emptyCatalog, true)).toContain("archive");
    expect(visibleStatusKeys(["archive"], [], emptyCatalog)).toEqual(["archive"]);
    expect(visibleStatusKeys(["cancelled"], [], emptyCatalog)).toEqual(["cancelled"]);
  });
});
