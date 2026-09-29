import { z } from "zod";
import type {
  IssueTemplate,
  IssueTemplateSummary,
  InstantiatedIssuePayload,
} from "../types";

const IssueTemplateSummarySchema = z.object({
  id: z.string(),
  workspace_id: z.string(),
  name: z.string(),
  issue_title: z.string(),
  config: z.record(z.string(), z.unknown()).default({}),
  created_by: z.string().nullable(),
  // Archived fields (RIC-906). Old servers omit them; default to false/null
  // so archived handling degrades to "everything active".
  archived: z.boolean().default(false),
  archived_at: z.string().nullable().default(null),
  created_at: z.string(),
  updated_at: z.string(),
});

export const IssueTemplateSummaryListSchema = z.array(IssueTemplateSummarySchema);

export const IssueTemplateDetailSchema = z.object({
  id: z.string(),
  workspace_id: z.string(),
  name: z.string(),
  issue_title: z.string(),
  issue_content: z.string(),
  config: z.record(z.string(), z.unknown()).default({}),
  created_by: z.string().nullable(),
  archived: z.boolean().default(false),
  archived_at: z.string().nullable().default(null),
  created_at: z.string(),
  updated_at: z.string(),
});

export const EMPTY_ISSUE_TEMPLATE_SUMMARY_LIST: IssueTemplateSummary[] = [];

export const EMPTY_ISSUE_TEMPLATE_DETAIL: IssueTemplate = {
  id: "",
  workspace_id: "",
  name: "",
  issue_title: "",
  issue_content: "",
  config: {},
  created_by: null,
  archived: false,
  archived_at: null,
  created_at: "",
  updated_at: "",
};

/**
 * Schema for `POST /api/issue-templates/{id}/instantiate`. The issue payload
 * intentionally keeps optional fields on the server contract loose (nullable
 * scalars) so a client never crashes when an older backend omits a default
 * that a newer template configured.
 */
export const InstantiatedIssuePayloadSchema = z.object({
  template_id: z.string(),
  name: z.string(),
  issue: z.object({
    title: z.string(),
    description: z.string(),
    priority: z.string().nullable().optional(),
    status: z.string().nullable().optional(),
    assignee_type: z.string().nullable().optional(),
    assignee_id: z.string().nullable().optional(),
    project_id: z.string().nullable().optional(),
    stage: z.number().nullable().optional(),
    start_date: z.string().nullable().optional(),
    due_date: z.string().nullable().optional(),
    label_ids: z.array(z.string()).optional(),
    properties: z.record(z.string(), z.unknown()).optional(),
    metadata: z.record(z.string(), z.unknown()).optional(),
  }),
  variables: z.record(z.string(), z.string()).default({}),
  unresolved_variables: z.array(z.string()).default([]),
});

export const EMPTY_INSTANTIATED_ISSUE_PAYLOAD: InstantiatedIssuePayload = {
  template_id: "",
  name: "",
  issue: {
    title: "",
    description: "",
  },
  variables: {},
  unresolved_variables: [],
};
