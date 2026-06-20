import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { loadDocsApi, __resetDocsApiLoaderForTest } from "./docs-api-loader";

describe("loadDocsApi", () => {
  beforeEach(() => {
    __resetDocsApiLoaderForTest();
    delete (window as unknown as { DocsAPI?: unknown }).DocsAPI;
  });
  afterEach(() => {
    document.head.querySelectorAll("script").forEach((s) => s.remove());
  });

  it("injects one script for concurrent calls and resolves with DocsAPI", async () => {
    const p1 = loadDocsApi("https://weboffice.x");
    const p2 = loadDocsApi("https://weboffice.x");
    const scripts = document.head.querySelectorAll('script[src*="api.js"]');
    expect(scripts.length).toBe(1);

    const fakeApi = { DocEditor: vi.fn() };
    (window as unknown as { DocsAPI: unknown }).DocsAPI = fakeApi;
    scripts[0]!.dispatchEvent(new Event("load"));

    await expect(p1).resolves.toBe(fakeApi);
    await expect(p2).resolves.toBe(fakeApi);
  });

  it("rejects on script error", async () => {
    const p = loadDocsApi("https://weboffice.y");
    const script = document.head.querySelector('script[src*="api.js"]')!;
    script.dispatchEvent(new Event("error"));
    await expect(p).rejects.toThrow(/failed to load/);
  });

  it("clears the cache when the script loads but DocsAPI is absent, so a retry re-injects", async () => {
    const p1 = loadDocsApi("https://weboffice.z");
    const first = document.head.querySelector('script[src*="api.js"]')!;
    // Script loads (200) but never sets window.DocsAPI (broken/partial bundle).
    first.dispatchEvent(new Event("load"));
    await expect(p1).rejects.toThrow(/DocsAPI not available/);

    // The failed promise must NOT stay cached — a retry injects a fresh script.
    const p2 = loadDocsApi("https://weboffice.z");
    expect(p2).not.toBe(p1);
    expect(
      document.head.querySelectorAll('script[src*="api.js"]').length,
    ).toBe(2);
  });
});
