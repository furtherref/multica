import { describe, expect, it } from "vitest";
import { parseWithFallback } from "../api/schema";
import {
  IssueTemplateSummaryListSchema,
  IssueTemplateDetailSchema,
  EMPTY_ISSUE_TEMPLATE_SUMMARY_LIST,
  EMPTY_ISSUE_TEMPLATE_DETAIL,
  InstantiatedIssuePayloadSchema,
  EMPTY_INSTANTIATED_ISSUE_PAYLOAD,
} from "./schemas";

const validSummary = {
  id: "tpl-1",
  workspace_id: "ws-1",
  name: "Bug report",
  issue_title: "Investigate {{area}} bug",
  config: {},
  created_by: "user-1",
  created_at: "2026-05-12T00:00:00Z",
  updated_at: "2026-05-12T00:00:00Z",
};

const validDetail = {
  ...validSummary,
  issue_content: "## Context\n\nSteps to reproduce",
};

describe("IssueTemplateSummaryListSchema", () => {
  it("parses a valid list response", () => {
    const result = parseWithFallback(
      [validSummary],
      IssueTemplateSummaryListSchema,
      EMPTY_ISSUE_TEMPLATE_SUMMARY_LIST,
      { endpoint: "listIssueTemplates" },
    );
    expect(result).toHaveLength(1);
    expect(result[0]!.name).toBe("Bug report");
  });

  it("falls back to empty array on non-array body", () => {
    const result = parseWithFallback(
      null,
      IssueTemplateSummaryListSchema,
      EMPTY_ISSUE_TEMPLATE_SUMMARY_LIST,
      { endpoint: "listIssueTemplates" },
    );
    expect(result).toEqual([]);
  });

  it("falls back to empty array on malformed items", () => {
    const result = parseWithFallback(
      [{ id: "x" }],
      IssueTemplateSummaryListSchema,
      EMPTY_ISSUE_TEMPLATE_SUMMARY_LIST,
      { endpoint: "listIssueTemplates" },
    );
    expect(result).toEqual([]);
  });

  it("tolerates missing optional fields (created_by null)", () => {
    const result = parseWithFallback(
      [{ ...validSummary, created_by: null }],
      IssueTemplateSummaryListSchema,
      EMPTY_ISSUE_TEMPLATE_SUMMARY_LIST,
      { endpoint: "listIssueTemplates" },
    );
    expect(result).toHaveLength(1);
    expect(result[0]!.created_by).toBeNull();
  });

  it("defaults archived fields when the server omits them", () => {
    const result = parseWithFallback(
      [validSummary],
      IssueTemplateSummaryListSchema,
      EMPTY_ISSUE_TEMPLATE_SUMMARY_LIST,
      { endpoint: "listIssueTemplates" },
    );
    expect(result[0]!.archived).toBe(false);
    expect(result[0]!.archived_at).toBeNull();
  });

  it("parses an archived template with archived fields set", () => {
    const result = parseWithFallback(
      [{ ...validSummary, archived: true, archived_at: "2026-05-13T00:00:00Z" }],
      IssueTemplateSummaryListSchema,
      EMPTY_ISSUE_TEMPLATE_SUMMARY_LIST,
      { endpoint: "listIssueTemplates" },
    );
    expect(result[0]!.archived).toBe(true);
    expect(result[0]!.archived_at).toBe("2026-05-13T00:00:00Z");
  });
});

describe("IssueTemplateDetailSchema", () => {
  it("parses a valid detail response", () => {
    const result = parseWithFallback(
      validDetail,
      IssueTemplateDetailSchema,
      EMPTY_ISSUE_TEMPLATE_DETAIL,
      { endpoint: "getIssueTemplate" },
    );
    expect(result.id).toBe("tpl-1");
    expect(result.issue_content).toBe("## Context\n\nSteps to reproduce");
  });

  it("falls back on missing required fields", () => {
    const result = parseWithFallback(
      { id: "x" },
      IssueTemplateDetailSchema,
      EMPTY_ISSUE_TEMPLATE_DETAIL,
      { endpoint: "getIssueTemplate" },
    );
    expect(result).toEqual(EMPTY_ISSUE_TEMPLATE_DETAIL);
  });

  it("falls back on null body", () => {
    const result = parseWithFallback(
      null,
      IssueTemplateDetailSchema,
      EMPTY_ISSUE_TEMPLATE_DETAIL,
      { endpoint: "getIssueTemplate" },
    );
    expect(result).toEqual(EMPTY_ISSUE_TEMPLATE_DETAIL);
  });
});

describe("InstantiatedIssuePayloadSchema", () => {
  const validInstantiated = {
    template_id: "tpl-1",
    name: "Bug report",
    issue: {
      title: "[backend] Fix P1 bug",
      description: "Area: backend\n\n## Steps",
      priority: "high",
      status: "todo",
      project_id: "proj-1",
      stage: 2,
      start_date: "2026-09-23",
      due_date: "2026-10-01",
      assignee_type: "agent",
      assignee_id: "agent-1",
      label_ids: ["lbl-1"],
      properties: { prop: "value" },
      metadata: { pipeline_status: "planned" },
    },
    variables: { area: "backend", sev: "P1" },
    unresolved_variables: [],
  };

  it("parses a fully-prefilled payload", () => {
    const result = parseWithFallback(
      validInstantiated,
      InstantiatedIssuePayloadSchema,
      EMPTY_INSTANTIATED_ISSUE_PAYLOAD,
      { endpoint: "instantiateIssueTemplate" },
    );
    expect(result.issue.title).toBe("[backend] Fix P1 bug");
    expect(result.issue.priority).toBe("high");
    expect(result.issue.stage).toBe(2);
    expect(result.variables.area).toBe("backend");
  });

  it("tolerates omitted optional defaults", () => {
    const result = parseWithFallback(
      {
        template_id: "tpl-1",
        name: "Bug report",
        issue: { title: "t", description: "d" },
        variables: {},
        unresolved_variables: [],
      },
      InstantiatedIssuePayloadSchema,
      EMPTY_INSTANTIATED_ISSUE_PAYLOAD,
      { endpoint: "instantiateIssueTemplate" },
    );
    expect(result.issue.title).toBe("t");
    expect(result.issue.priority).toBeUndefined();
  });

  it("falls back on null body", () => {
    const result = parseWithFallback(
      null,
      InstantiatedIssuePayloadSchema,
      EMPTY_INSTANTIATED_ISSUE_PAYLOAD,
      { endpoint: "instantiateIssueTemplate" },
    );
    expect(result).toEqual(EMPTY_INSTANTIATED_ISSUE_PAYLOAD);
  });
});
