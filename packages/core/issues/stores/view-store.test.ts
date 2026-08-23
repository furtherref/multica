// @vitest-environment jsdom
import { afterEach, beforeAll, beforeEach, describe, expect, it } from "vitest";
import { useIssueViewStore } from "./view-store";
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
  // Hiding a column is display state (`hiddenStatusCategories`) and is kept
  // apart from the status FILTER (MUL-6243). The fork depends on that
  // separation: `archive` (status #39) is opt-in, and a board control that
  // reseeded `statusFilters` from the full category list would enroll it
  // without the user ever asking for archived work.
  it("hideStatus records a hidden column without touching the status filter", () => {
    useIssueViewStore.getState().hideStatus("todo");
    expect(useIssueViewStore.getState().hiddenStatusCategories).toEqual(["todo"]);
    expect(useIssueViewStore.getState().statusFilters).toEqual([]);
  });

  it("hideStatus leaves an already-active filter set alone", () => {
    useIssueViewStore.setState({ statusFilters: ["todo", "in_progress", "archive"] });
    useIssueViewStore.getState().hideStatus("todo");
    expect(useIssueViewStore.getState().statusFilters).toEqual([
      "todo",
      "in_progress",
      "archive",
    ]);
    expect(useIssueViewStore.getState().hiddenStatusCategories).toEqual(["todo"]);
  });

  it("showStatus un-hides a column and never seeds a status filter", () => {
    useIssueViewStore.setState({ hiddenStatusCategories: ["todo", "done"] });
    useIssueViewStore.getState().showStatus("todo");
    expect(useIssueViewStore.getState().hiddenStatusCategories).toEqual(["done"]);
    expect(useIssueViewStore.getState().statusFilters).toEqual([]);
  });
});
