-- 000006_project: projects + states (spec §4, Task 12).
--
-- projects.identifier is VARCHAR(12), UNIQUE per workspace. The service
-- uppercases identifiers before insert ("eng" -> "ENG"), so
-- case-insensitive conflicts surface as 23505 and map to
-- ErrIdentifierConflict. archive_in_days / close_in_days are consumed by
-- later phases (Task 22 uses close_in_days for cycle rollover); they stay
-- NULL until set.
CREATE TABLE IF NOT EXISTS projects (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id    UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    identifier      VARCHAR(12) NOT NULL,
    name            TEXT NOT NULL,
    description     TEXT NOT NULL DEFAULT '',
    view_flags      JSONB,
    archive_in_days INT,
    close_in_days   INT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, identifier)
);
-- Lookup: "projects of a workspace".
CREATE INDEX IF NOT EXISTS projects_workspace_idx ON projects (workspace_id);

-- states.group is constrained to the Plane group vocabulary ("group" is
-- quoted: reserved word). sequence orders the kanban columns.
-- UNIQUE(project_id, name) prevents duplicate columns in one project.
CREATE TABLE IF NOT EXISTS states (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    "group"    TEXT NOT NULL CHECK ("group" IN ('triage','backlog','unstarted','started','completed','cancelled')),
    color      TEXT NOT NULL DEFAULT '',
    sequence   INT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (project_id, name)
);
-- Lookup: "states of a project, in column order".
CREATE INDEX IF NOT EXISTS states_project_idx ON states (project_id);
