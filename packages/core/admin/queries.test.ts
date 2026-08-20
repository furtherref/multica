// @vitest-environment node
import { beforeEach, describe, expect, it, vi } from "vitest";

const mockGetAdminUsers = vi.hoisted(() => vi.fn());

vi.mock("../api", () => ({
  api: { getAdminUsers: mockGetAdminUsers },
}));

import { adminKeys, adminUsersOptions } from "./queries";

describe("adminKeys.users", () => {
  it("keys each server-side status filter separately", () => {
    expect(adminKeys.users("active")).toEqual(["admin", "users", "active"]);
    expect(adminKeys.users("suspended")).toEqual(["admin", "users", "suspended"]);
    expect(adminKeys.users("all")).toEqual(["admin", "users", "all"]);
  });

  it("yields the bare prefix without a status, so one invalidation hits every filter", () => {
    expect(adminKeys.users()).toEqual(["admin", "users"]);
  });
});

describe("adminUsersOptions", () => {
  beforeEach(() => {
    mockGetAdminUsers.mockReset();
    mockGetAdminUsers.mockResolvedValue([]);
  });

  it("puts the status in the query key and passes it to the API call", async () => {
    const options = adminUsersOptions("suspended");
    expect(options.queryKey).toEqual(["admin", "users", "suspended"]);

    await (options.queryFn as () => Promise<unknown>)();
    expect(mockGetAdminUsers).toHaveBeenCalledWith("suspended");
  });

  it("requests active accounts when asked for the default view", async () => {
    const options = adminUsersOptions("active");
    expect(options.queryKey).toEqual(["admin", "users", "active"]);

    await (options.queryFn as () => Promise<unknown>)();
    expect(mockGetAdminUsers).toHaveBeenCalledWith("active");
  });
});
