import { z } from "zod";
import type { IssueTemplate, IssueTemplateSummary } from "../types";

const IssueTemplateSummarySchema = z.object({
  id: z.string(),
  workspace_id: z.string(),
  name: z.string(),
  issue_title: z.string(),
  config: z.record(z.string(), z.unknown()).default({}),
  created_by: z.string().nullable(),
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
  created_at: "",
  updated_at: "",
};
