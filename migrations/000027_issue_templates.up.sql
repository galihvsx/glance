-- 000027_issue_templates: project issue templates (C7T0).
--
-- issue_templates: named, reusable issue blueprints scoped to a project.
-- template_data is a JSONB document holding the issue defaults applied
-- by POST .../templates/{id}/apply:
--
--   { "name":            "optional default issue name",
--     "description":     <any JSON value, passed through to the issue>,
--     "priority":        0-4,
--     "estimate_point_id": "<uuid of a project estimate point>",
--     "label_ids":       ["<workspace label uuid>", ...],
--     "state_id":        "<uuid of a project state>" }
--
-- Refs are validated at apply time, not here: a state/label/estimate may
-- be renamed or deleted after the template is saved, and apply must fail
-- loudly (404, stale ref) instead of silently creating a wrong issue.
--
-- Everything is IF NOT EXISTS so the migration is re-runnable.
CREATE TABLE IF NOT EXISTS issue_templates (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id    UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name          TEXT NOT NULL,
    description   TEXT NOT NULL DEFAULT '',
    template_data JSONB NOT NULL DEFAULT '{}',
    created_by    UUID,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (project_id, name)
);
CREATE INDEX IF NOT EXISTS idx_issue_templates_project
    ON issue_templates(project_id, created_at DESC);
