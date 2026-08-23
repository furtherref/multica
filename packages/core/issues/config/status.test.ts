// @vitest-environment node
import { describe, expect, it } from "vitest";
import {
  ALL_STATUSES,
  BOARD_STATUSES,
  DEFAULT_VISIBLE_STATUSES,
  STATUS_CONFIG,
  STATUS_ORDER,
} from "./status";

describe("issue status config", () => {
  // `archive` (fork status #39) has to render — as a column, an icon and a
  // label — without being offerable as a CATALOG category. The server refuses
  // it as one (issuestatus.Canonical), so a custom status can never inherit it,
  // and the settings UI must never list it. That split is exactly STATUS_ORDER
  // (everything that renders) vs ALL_STATUSES (the 7 catalog categories).
  it("renders archive as its own column but never as a catalog category", () => {
    expect(STATUS_ORDER).toContain("archive");
    expect(ALL_STATUSES).not.toContain("archive");
    expect(ALL_STATUSES).toHaveLength(7);
    expect(STATUS_CONFIG.archive.label).toBe("Archive");
  });

  it("keeps archive out of the default board and fetch sets", () => {
    expect(BOARD_STATUSES).not.toContain("archive");
    expect(DEFAULT_VISIBLE_STATUSES).not.toContain("archive");
  });

  it("orders archive last, after every catalog category", () => {
    expect(STATUS_ORDER.slice(0, -1)).toEqual(ALL_STATUSES);
    expect(STATUS_ORDER.at(-1)).toBe("archive");
  });
});
