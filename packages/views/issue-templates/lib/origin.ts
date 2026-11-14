import type { IssueTemplateSummary } from "@multica/core/types";

export type IssueTemplateOrigin = {
  type: "manual";
};

export function readIssueTemplateOrigin(_template: IssueTemplateSummary): IssueTemplateOrigin {
  return { type: "manual" };
}
