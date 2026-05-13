import type { InboxItem } from "../types";

export function resolveInboxEventTarget(
  item: InboxItem,
  current: {
    currentWorkspaceId: string | null;
    currentWorkspaceSlug: string | null;
  },
): { workspaceId: string | null; workspaceSlug: string | null } {
  return {
    workspaceId: item.workspace_id || current.currentWorkspaceId,
    workspaceSlug: item.workspace_slug || current.currentWorkspaceSlug,
  };
}
