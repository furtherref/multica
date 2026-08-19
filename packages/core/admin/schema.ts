import { z } from "zod";

// ---------------------------------------------------------------------------
// System-admin user listing (`GET /api/admin/users`) and status update
// (`PATCH /api/admin/users/{id}/status`). Only reachable by users with
// `is_system_admin === true` on their `/api/me` response; kept lenient by the
// same rules as every other endpoint schema (see api/schemas.ts UserSchema
// comment) — unrecognized `account_status` values degrade to `"unknown"`
// (server-driven enum, default branch) instead of throwing.
// ---------------------------------------------------------------------------

export const adminAccountStatusSchema = z
  .string()
  .transform((s) => (s === "active" || s === "suspended" ? s : ("unknown" as const)));

export const adminUserSchema = z.object({
  id: z.string(),
  name: z.string().default(""),
  email: z.string().default(""),
  avatar_url: z.string().nullish().transform((v) => v ?? null),
  account_status: adminAccountStatusSchema.default("unknown"),
  created_at: z.string().default(""),
});

export const adminUserListSchema = z.object({
  users: z.array(adminUserSchema).default([]),
});

export type AdminUser = z.infer<typeof adminUserSchema>;

export const EMPTY_ADMIN_USER: AdminUser = {
  id: "",
  name: "",
  email: "",
  avatar_url: null,
  account_status: "unknown",
  created_at: "",
};
