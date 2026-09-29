export interface IssueTemplate {
  id: string;
  workspace_id: string;
  name: string;
  issue_title: string;
  issue_content: string;
  config: Record<string, unknown>;
  created_by: string | null;
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

/** Template variable definition declared in `IssueTemplate.config.variables`. */
export interface IssueTemplateVariableDefinition {
  label?: string;
  required?: boolean;
  default?: unknown;
}

/**
 * Defaults declared in `IssueTemplate.config.defaults`, applied by the
 * instantiate endpoint while pre-filling a new-issue payload. Paths mirror the
 * create-issue request contract.
 */
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

/**
 * Response of `POST /api/issue-templates/{id}/instantiate`. `issue` mirrors the
 * create-issue request wire shape so a client can feed it straight into the
 * create-issue form (and ultimately `POST /api/issues`).
 */
export interface InstantiatedIssuePayload {
  template_id: string;
  name: string;
  issue: {
    title: string;
    description: string;
    priority?: string | null;
    status?: string | null;
    assignee_type?: string | null;
    assignee_id?: string | null;
    project_id?: string | null;
    stage?: number | null;
    start_date?: string | null;
    due_date?: string | null;
    label_ids?: string[];
    properties?: Record<string, unknown>;
    metadata?: Record<string, unknown>;
  };
  variables: Record<string, string>;
  unresolved_variables: string[];
}

export interface InstantiateIssueTemplateRequest {
  variables: Record<string, unknown>;
}
