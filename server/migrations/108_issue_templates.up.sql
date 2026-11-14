CREATE TABLE issue_template (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    issue_title TEXT NOT NULL,
    issue_content TEXT NOT NULL DEFAULT '',
    config JSONB NOT NULL DEFAULT '{}',
    created_by UUID REFERENCES "user"(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(workspace_id, name)
);

CREATE INDEX idx_issue_template_workspace ON issue_template(workspace_id);
