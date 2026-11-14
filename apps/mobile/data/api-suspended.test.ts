/**
 * Mirrors packages/core/api/client-suspended.test.ts: a suspended account
 * gets a 403 `{"code":"ACCOUNT_SUSPENDED"}` from every authenticated
 * endpoint. Mobile reuses the existing `onUnauthorized` logout+redirect
 * flow (`_layout.tsx`) for that response, same as a plain 401.
 *
 * `./workspace-store` is mocked because it pulls in `expo-secure-store`
 * (a native module) — per vitest.config.ts, data-layer tests that load
 * `@/data/api` must mock that dependency so the native chain never loads.
 * `EXPO_PUBLIC_API_URL` is stubbed + the module re-imported per test
 * because `api.ts` throws at import time if the env var is unset.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("./workspace-store", () => ({
  getCurrentSlug: () => null,
}));

const ORIGINAL_API_URL = process.env.EXPO_PUBLIC_API_URL;

beforeEach(() => {
  process.env.EXPO_PUBLIC_API_URL = "https://api.example.test";
  vi.resetModules();
});

afterEach(() => {
  process.env.EXPO_PUBLIC_API_URL = ORIGINAL_API_URL;
  vi.unstubAllGlobals();
});

describe("ApiClient account suspension", () => {
  it("fires onUnauthorized on a 403 carrying code ACCOUNT_SUSPENDED", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        new Response(
          JSON.stringify({ error: "account suspended", code: "ACCOUNT_SUSPENDED" }),
          { status: 403, headers: { "Content-Type": "application/json" } },
        ),
      ),
    );

    const { api, ApiError } = await import("./api");
    const onUnauthorized = vi.fn();
    api.setOptions({ onUnauthorized });
    api.setToken("some-token");

    await expect(api.getMe()).rejects.toBeInstanceOf(ApiError);
    await expect(
      api.getMe().catch((e) => e),
    ).resolves.toMatchObject({ status: 403 });
    expect(onUnauthorized).toHaveBeenCalled();
  });

  it("does not fire onUnauthorized on a plain 403 without the ACCOUNT_SUSPENDED code", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        new Response(JSON.stringify({ error: "forbidden" }), {
          status: 403,
          headers: { "Content-Type": "application/json" },
        }),
      ),
    );

    const { api } = await import("./api");
    const onUnauthorized = vi.fn();
    api.setOptions({ onUnauthorized });
    api.setToken("some-token");

    await expect(api.getMe()).rejects.toMatchObject({ status: 403 });
    expect(onUnauthorized).not.toHaveBeenCalled();
  });

  it("still fires onUnauthorized on a plain 401 (existing behavior unchanged)", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        new Response(JSON.stringify({ error: "unauthorized" }), {
          status: 401,
          headers: { "Content-Type": "application/json" },
        }),
      ),
    );

    const { api } = await import("./api");
    const onUnauthorized = vi.fn();
    api.setOptions({ onUnauthorized });
    api.setToken("some-token");

    await expect(api.getMe()).rejects.toMatchObject({ status: 401 });
    expect(onUnauthorized).toHaveBeenCalled();
  });
});
