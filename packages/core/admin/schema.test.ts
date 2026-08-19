// @vitest-environment node
import { describe, expect, it } from "vitest";
import { adminUserListSchema, adminUserSchema } from "./schema";

describe("adminUserSchema", () => {
  it("parses a valid admin user payload", () => {
    const result = adminUserSchema.safeParse({
      id: "user-1",
      name: "Ada Lovelace",
      email: "ada@example.test",
      avatar_url: "https://example.test/a.png",
      account_status: "active",
      created_at: "2026-01-01T00:00:00Z",
    });
    expect(result.success).toBe(true);
    expect(result.data).toEqual({
      id: "user-1",
      name: "Ada Lovelace",
      email: "ada@example.test",
      avatar_url: "https://example.test/a.png",
      account_status: "active",
      created_at: "2026-01-01T00:00:00Z",
    });
  });

  it("maps an unrecognized account_status to 'unknown'", () => {
    const result = adminUserSchema.safeParse({
      id: "user-2",
      name: "Bob",
      email: "bob@example.test",
      avatar_url: null,
      account_status: "banned",
      created_at: "2026-01-01T00:00:00Z",
    });
    expect(result.success).toBe(true);
    expect(result.data?.account_status).toBe("unknown");
  });

  it("defaults account_status to 'unknown' when missing", () => {
    const result = adminUserSchema.safeParse({
      id: "user-3",
      email: "carol@example.test",
    });
    expect(result.success).toBe(true);
    expect(result.data?.account_status).toBe("unknown");
    expect(result.data?.name).toBe("");
  });

  it("normalizes a missing avatar_url to null", () => {
    const result = adminUserSchema.safeParse({
      id: "user-4",
      account_status: "suspended",
    });
    expect(result.success).toBe(true);
    expect(result.data?.avatar_url).toBeNull();
  });
});

describe("adminUserListSchema", () => {
  it("parses a valid list", () => {
    const result = adminUserListSchema.safeParse({
      users: [
        {
          id: "user-1",
          name: "Ada",
          email: "ada@example.test",
          avatar_url: null,
          account_status: "active",
          created_at: "2026-01-01T00:00:00Z",
        },
      ],
    });
    expect(result.success).toBe(true);
    expect(result.data?.users).toHaveLength(1);
  });

  it("degrades to an empty list when 'users' is missing", () => {
    const result = adminUserListSchema.safeParse({});
    expect(result.success).toBe(true);
    expect(result.data?.users).toEqual([]);
  });

  it("degrades to an empty list when the payload shape is wrong", () => {
    const result = adminUserListSchema.safeParse({ users: "not-an-array" });
    expect(result.success).toBe(false);
  });

  it("drops a malformed entry's whole-list validity but still exposes a safe fallback via parseWithFallback", async () => {
    const { parseWithFallback } = await import("../api/schema");
    const parsed = parseWithFallback(
      { users: [{ id: 42, account_status: "active" }] },
      adminUserListSchema,
      { users: [] },
      { endpoint: "GET /api/admin/users" },
    );
    expect(parsed.users).toEqual([]);
  });
});
