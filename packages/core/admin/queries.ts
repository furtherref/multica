import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";
import type { AdminAccountStatusFilter } from "./schema";

/**
 * System-admin user management queries. Admin data is global, not
 * workspace-scoped — the query key intentionally carries no `wsId`.
 *
 * The listing is filtered server-side by account status, so the filter is
 * part of the query key: each status gets its own cache entry, and calling
 * `adminKeys.users()` without an argument yields the prefix that matches
 * every status (for invalidation after a status change).
 */
export const adminKeys = {
  users: (status?: AdminAccountStatusFilter) =>
    status
      ? (["admin", "users", status] as const)
      : (["admin", "users"] as const),
};

export function adminUsersOptions(status: AdminAccountStatusFilter) {
  return queryOptions({
    queryKey: adminKeys.users(status),
    queryFn: () => api.getAdminUsers(status),
  });
}
