import { useAuthStore } from "@multica/core/auth";
import { AdminUsersPage } from "@multica/views/admin";
import { DragStrip } from "@multica/views/platform";

/**
 * `/admin` is a session-level full-window view outside the dashboard shell
 * (system administration is global, not workspace-scoped), so it mounts its
 * own `<DragStrip />` as the first flex child rather than relying on
 * `WorkspaceRouteLayout` — see CLAUDE.md "Full-window desktop views".
 *
 * Unlike WorkspaceRouteLayout (which never mounts unauthenticated — App.tsx
 * renders <DesktopLoginPage> instead), this route IS reachable while signed
 * in as a non-admin: the sidebar entry that links here is gated on
 * `is_system_admin === true`, but nothing stops a signed-in non-admin from
 * still holding a stale/persisted tab pointed at "/admin" (e.g. after a
 * workspace owner's admin flag is revoked). Mirror the web guard
 * (apps/web/app/admin/page.tsx) and render nothing while loading or when the
 * signed-in user is not an admin — the server still 403s /api/admin/* calls
 * regardless, so this is a UX nicety, not the security boundary.
 */
export function AdminPage() {
  const user = useAuthStore((s) => s.user);
  const isAuthLoading = useAuthStore((s) => s.isLoading);

  if (isAuthLoading) return null;
  if (!user || user.is_system_admin !== true) return null;

  return (
    <div className="flex h-screen flex-col overflow-hidden">
      <DragStrip />
      <div className="flex-1 overflow-y-auto">
        <AdminUsersPage />
      </div>
    </div>
  );
}
