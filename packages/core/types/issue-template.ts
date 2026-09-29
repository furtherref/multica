export interface IssueTemplate {
  id: string;
  workspace_id: string;
  name: string;
  issue_title: string;
  issue_content: string;
  config: Record<string, unknown>;
  created_by: string | null;
  enabled: boolean;
  position: number;
  archived: boolean;
  archived_at: string | null;
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
  enabled: boolean;
  position: number;
  archived: boolean;
  archived_at: string | null;
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

/** A declared {{variable}} in an issue template's title/content. */
export interface IssueTemplateVariableDefinition {
  label?: string;
  required?: boolean;
  default?: unknown;
}

/** `config.variables` — declared template variables, keyed by variable name. */
export type IssueTemplateVariables = Record<string, IssueTemplateVariableDefinition>;

/** `config.defaults` — pre-fill values applied when instantiating a template. */
export interface IssueTemplateDefaults {
  priority?: string;
  status?: string;
  assignee_type?: string;
  assignee_id?: string;
  project_id?: string;
  stage?: number;
  start_date_offset_days?: number;
  due_date_offset_days?: number;
  properties?: Record<string, unknown>;
  metadata?: Record<string, unknown>;
  labels?: string[];
}

/** Structured `config` shape for an issue template. */
export interface IssueTemplateConfig {
  variables?: IssueTemplateVariables;
  defaults?: IssueTemplateDefaults;
}
