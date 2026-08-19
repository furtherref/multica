import { AdminUsersPage } from "@multica/views/admin";
import { DragStrip } from "@multica/views/platform";

/**
 * `/admin` is a session-level full-window view outside the dashboard shell
 * (system administration is global, not workspace-scoped), so it mounts its
 * own `<DragStrip />` as the first flex child rather than relying on
 * `WorkspaceRouteLayout` — see CLAUDE.md "Full-window desktop views".
 */
export function AdminPage() {
  return (
    <div className="flex h-screen flex-col overflow-hidden">
      <DragStrip />
      <div className="flex-1 overflow-y-auto">
        <AdminUsersPage />
      </div>
    </div>
  );
}
