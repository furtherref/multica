export interface IssueTemplate {
  id: string;
  workspace_id: string;
  name: string;
  issue_title: string;
  issue_content: string;
  config: Record<string, unknown>;
  created_by: string | null;
  created_at: string;
  updated_at: string;
}

export interface IssueTemplateSummary {
  id: string;
  workspace_id: string;
  name: string;
  issue_title: string;
  config: Record<string, unknown>;
  created_by: string | null;
  created_at: string;
  updated_at: string;
}

export interface CreateIssueTemplateRequest {
  name: string;
  issue_title: string;
  issue_content?: string;
  config?: Record<string, unknown>;
}

export interface UpdateIssueTemplateRequest {
  name?: string;
  issue_title?: string;
  issue_content?: string;
  config?: Record<string, unknown>;
}
