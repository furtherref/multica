import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { useWorkspaceId } from "../hooks";
import type { CreateIssueTemplateRequest, IssueTemplate, UpdateIssueTemplateRequest } from "../types";
import { issueTemplateKeys } from "./queries";

export function useCreateIssueTemplate() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();

  return useMutation({
    mutationFn: (data: CreateIssueTemplateRequest) => api.createIssueTemplate(data),
    onSuccess: (template) => {
      qc.setQueryData<IssueTemplate[]>(issueTemplateKeys.list(wsId), (old) =>
        old && !old.some((item) => item.id === template.id)
          ? [...old, template].sort((a, b) => a.name.localeCompare(b.name))
          : old,
      );
      qc.setQueryData(issueTemplateKeys.detail(wsId, template.id), template);
    },
    onSettled: () => {
      qc.invalidateQueries({ queryKey: issueTemplateKeys.list(wsId) });
    },
  });
}

export function useUpdateIssueTemplate() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();

  return useMutation({
    mutationFn: ({ id, ...data }: { id: string } & UpdateIssueTemplateRequest) =>
      api.updateIssueTemplate(id, data),
    onSuccess: (template) => {
      qc.setQueryData<IssueTemplate[]>(issueTemplateKeys.list(wsId), (old) =>
        old ? old.map((item) => (item.id === template.id ? template : item)) : old,
      );
      qc.setQueryData(issueTemplateKeys.detail(wsId, template.id), template);
    },
    onSettled: (_data, _err, vars) => {
      qc.invalidateQueries({ queryKey: issueTemplateKeys.list(wsId) });
      qc.invalidateQueries({ queryKey: issueTemplateKeys.detail(wsId, vars.id) });
    },
  });
}

export function useDeleteIssueTemplate() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();

  return useMutation({
    mutationFn: (id: string) => api.deleteIssueTemplate(id),
    onMutate: async (id) => {
      await qc.cancelQueries({ queryKey: issueTemplateKeys.list(wsId) });
      const prevList = qc.getQueryData<IssueTemplate[]>(issueTemplateKeys.list(wsId));
      qc.setQueryData<IssueTemplate[]>(issueTemplateKeys.list(wsId), (old) =>
        old ? old.filter((item) => item.id !== id) : old,
      );
      qc.removeQueries({ queryKey: issueTemplateKeys.detail(wsId, id) });
      return { prevList };
    },
    onError: (_err, _id, ctx) => {
      if (ctx?.prevList) qc.setQueryData(issueTemplateKeys.list(wsId), ctx.prevList);
    },
    onSettled: () => {
      qc.invalidateQueries({ queryKey: issueTemplateKeys.list(wsId) });
    },
  });
}
