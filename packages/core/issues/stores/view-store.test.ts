// @vitest-environment jsdom
import { afterEach, beforeAll, beforeEach, describe, expect, it } from "vitest";
import { useIssueViewStore } from "./view-store";
import { DEFAULT_VISIBLE_STATUSES } from "../config";
import { setCurrentWorkspace } from "../../platform/workspace-storage";

// Node 25 ships a partial `localStorage` shim under jsdom that's missing
// `clear`/`removeItem`; replace it with a real in-memory Storage so persist
// can round-trip values.
beforeAll(() => {
  if (typeof globalThis.localStorage?.clear !== "function") {
    const values = new Map<string, string>();
    const storage: Storage = {
      get length() { return values.size; },
      clear: () => values.clear(),
      getItem: (k) => values.get(k) ?? null,
      key: (i) => Array.from(values.keys())[i] ?? null,
      removeItem: (k) => { values.delete(k); },
      setItem: (k, v) => { values.set(k, v); },
    };
    Object.defineProperty(globalThis, "localStorage", { configurable: true, value: storage });
    Object.defineProperty(window, "localStorage", { configurable: true, value: storage });
  }
});

beforeEach(() => {
  localStorage.clear();
  useIssueViewStore.setState({ statusFilters: [] });
  setCurrentWorkspace(null, null);
});

afterEach(() => {
  setCurrentWorkspace(null, null);
});

describe("useIssueViewStore hideStatus / showStatus", () => {
  it("hideStatus from the default view never implicitly selects archive", () => {
    // no active filter
    useIssueViewStore.getState().hideStatus("todo");
    const filters = useIssueViewStore.getState().statusFilters;
    expect(filters).not.toContain("archive");
    expect(filters).toEqual(DEFAULT_VISIBLE_STATUSES.filter((s) => s !== "todo"));
  });

  it("hideStatus narrows an already-active filter set without reseeding", () => {
    useIssueViewStore.setState({ statusFilters: ["todo", "in_progress", "archive"] });
    useIssueViewStore.getState().hideStatus("todo");
    expect(useIssueViewStore.getState().statusFilters).toEqual([
      "in_progress",
      "archive",
    ]);
  });

  it("showStatus is a no-op when no filter is active (never seeds archive)", () => {
    useIssueViewStore.getState().showStatus("archive");
    expect(useIssueViewStore.getState().statusFilters).toEqual([]);
  });

  it("showStatus adds the status to an already-active filter set", () => {
    useIssueViewStore.setState({ statusFilters: ["todo"] });
    useIssueViewStore.getState().showStatus("archive");
    expect(useIssueViewStore.getState().statusFilters).toEqual(["todo", "archive"]);
  });
});
