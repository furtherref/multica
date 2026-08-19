// @vitest-environment node
import { afterEach, describe, expect, it, vi } from "vitest";
import { setCurrentWorkspace } from "../platform/workspace-storage";
import { ApiClient, ApiError } from "./client";

afterEach(() => {
  setCurrentWorkspace(null, null);
  vi.unstubAllGlobals();
});

describe("ApiClient session rejection", () => {
  it("fires onSessionRejected('account_suspended') and throws a 403 ApiError on ACCOUNT_SUSPENDED", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        new Response(
          JSON.stringify({ error: "account suspended", code: "ACCOUNT_SUSPENDED" }),
          { status: 403, headers: { "Content-Type": "application/json" } },
        ),
      ),
    );

    const onSessionRejected = vi.fn();
    const client = new ApiClient("https://api.example.test", { onSessionRejected });
    client.setToken("some-token");

    await expect(client.getMe()).rejects.toMatchObject({
      status: 403,
    });
    await expect(client.getMe()).rejects.toBeInstanceOf(ApiError);
    expect(onSessionRejected).toHaveBeenCalledWith("account_suspended");
  });

  it("does not fire onSessionRejected on a plain 403 without the ACCOUNT_SUSPENDED code", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        new Response(
          JSON.stringify({ error: "forbidden" }),
          { status: 403, headers: { "Content-Type": "application/json" } },
        ),
      ),
    );

    const onSessionRejected = vi.fn();
    const client = new ApiClient("https://api.example.test", { onSessionRejected });

    await expect(client.getMe()).rejects.toMatchObject({ status: 403 });
    expect(onSessionRejected).not.toHaveBeenCalled();
  });

  it("fires onSessionRejected('unauthorized') and still calls onUnauthorized on 401", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        new Response(JSON.stringify({ error: "unauthorized" }), {
          status: 401,
          headers: { "Content-Type": "application/json" },
        }),
      ),
    );

    const onSessionRejected = vi.fn();
    const onUnauthorized = vi.fn();
    const client = new ApiClient("https://api.example.test", {
      onSessionRejected,
      onUnauthorized,
    });

    await expect(client.getMe()).rejects.toMatchObject({ status: 401 });
    expect(onSessionRejected).toHaveBeenCalledWith("unauthorized");
    expect(onUnauthorized).toHaveBeenCalled();
  });
});
