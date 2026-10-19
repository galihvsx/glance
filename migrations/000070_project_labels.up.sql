-- 000070_project_labels: workspace-level project-label taxonomy (C76T0).
--
-- Plane v2.5.0 headline #2: "Project Labels — Add labels to projects at
-- the workspace level to organize them by team, function, priority, or any
-- structure that fits your workflow." Labels are reusable across projects.
-- glance's existing labels table is a WORK-ITEM taxonomy (migration
-- 000008); this adds a separate project-label taxonomy. Flat — no
-- parent_id hierarchy (Plane's project labels have none; YAGNI).
--
-- Storage:
--   project_labels: workspace-scoped (workspace_id ON DELETE CASCADE),
--     UNIQUE (workspace_id, name) like the issue labels — a duplicate
--     create is 409.
--   project_project_labels: junction (project_id, label_id) PRIMARY KEY +
--     FKs ON DELETE CASCADE — deleting a project or a label drops its
--     junction rows only; the other side is never touched.
--
-- Idempotent: safe to re-run (IF NOT EXISTS everywhere).

CREATE TABLE IF NOT EXISTS project_labels (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    color        TEXT NOT NULL DEFAULT '#6b7280',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, name)
);
CREATE INDEX IF NOT EXISTS project_labels_workspace_idx
    ON project_labels (workspace_id);

CREATE TABLE IF NOT EXISTS project_project_labels (
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    label_id   UUID NOT NULL REFERENCES project_labels(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (project_id, label_id)
);
CREATE INDEX IF NOT EXISTS project_project_labels_project_idx
    ON project_project_labels (project_id);
