-- 000050_initiatives: initiatives (Plane "initiatives" / programs) are
-- workspace-scoped containers that group projects — unlike modules and
-- cycles, which are project-scoped. glance ships initiatives free (Plane
-- paywalls them; C27T0 is the backend only, UI follows).
--
-- initiatives.own NO issues: issue membership stays project-scoped, so
-- deleting an initiative touches only initiative_projects rows + the
-- initiative row itself. created_by is SET NULL (not CASCADE) so
-- deleting a user never deletes an initiative. Idempotent: safe to
-- re-run.
CREATE TABLE IF NOT EXISTS initiatives (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    description  TEXT,
    status       TEXT NOT NULL DEFAULT 'active'
                 CHECK (status IN ('active','completed','archived')),
    created_by   UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, name)
);
CREATE INDEX IF NOT EXISTS initiatives_workspace_idx
    ON initiatives (workspace_id, status);

CREATE TABLE IF NOT EXISTS initiative_projects (
    initiative_id UUID NOT NULL REFERENCES initiatives(id) ON DELETE CASCADE,
    project_id    UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    added_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (initiative_id, project_id)
);
CREATE INDEX IF NOT EXISTS initiative_projects_project_idx
    ON initiative_projects (project_id);
