-- C17T1: project templates.
--
-- project_templates are workspace-scoped blueprints for new projects. The
-- payload JSONB carries the seeds applied when a project is created with
-- template_id:
--   {
--     "states":         [{ "name": "Backlog", "group": "backlog", "color": "#6b7280" }],
--     "labels":         [{ "name": "bug", "color": "#ef4444" }],
--     "modules":        [{ "name": "API", "description": "..." }],
--     "estimate_scale": { "name": "Fibonacci", "points": [{ "key": "1", "value": 1, "description": "..." }] }
--   }
-- Distinct from C7T0's issue_templates (project-scoped issue defaults).
CREATE TABLE IF NOT EXISTS project_templates (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    description  TEXT NOT NULL DEFAULT '',
    payload      JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_by   UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT chk_project_templates_name CHECK (char_length(name) BETWEEN 1 AND 200)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_project_templates_ws_name
    ON project_templates (workspace_id, lower(name));
CREATE INDEX IF NOT EXISTS idx_project_templates_ws
    ON project_templates (workspace_id);
