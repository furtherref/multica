import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";

export const issueTemplateKeys = {
  all: (wsId: string) => ["issue-templates", wsId] as const,
  list: (wsId: string, includeArchived = false) =>
    [...issueTemplateKeys.all(wsId), "list", includeArchived] as const,
  detail: (wsId: string, id: string) =>
    [...issueTemplateKeys.all(wsId), "detail", id] as const,
};

export function issueTemplateListOptions(wsId: string, includeArchived = false) {
  return queryOptions({
    queryKey: issueTemplateKeys.list(wsId, includeArchived),
    queryFn: () => api.listIssueTemplates(includeArchived),
  });
}

export function issueTemplateDetailOptions(wsId: string, id: string) {
  return queryOptions({
    queryKey: issueTemplateKeys.detail(wsId, id),
    queryFn: () => api.getIssueTemplate(id),
    enabled: !!id,
  });
}
