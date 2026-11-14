import { describe, expect, it } from "vitest";
import { parseWithFallback } from "../api/schema";
import {
  IssueTemplateSummaryListSchema,
  IssueTemplateDetailSchema,
  EMPTY_ISSUE_TEMPLATE_SUMMARY_LIST,
  EMPTY_ISSUE_TEMPLATE_DETAIL,
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
