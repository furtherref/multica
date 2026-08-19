import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";

/**
 * System-admin user management queries. Admin data is global, not
 * workspace-scoped — the query key intentionally carries no `wsId`.
 */
export const adminKeys = {
  users: () => ["admin", "users"] as const,
};

export function adminUsersOptions() {
  return queryOptions({
    queryKey: adminKeys.users(),
    queryFn: () => api.getAdminUsers(),
  });
}
