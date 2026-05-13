import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";

export const notificationPreferenceKeys = {
  all: (wsId: string) => ["notification-preferences", wsId] as const,
};

export function notificationPreferenceOptions(wsId: string, workspaceSlug?: string | null) {
  return queryOptions({
    queryKey: notificationPreferenceKeys.all(wsId),
    queryFn: () => api.getNotificationPreferences(workspaceSlug ?? undefined),
  });
}
