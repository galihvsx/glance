-- 000069_epics: the epic entity (C71T0).
--
-- Plane's Epics are a first-class planning entity: a long-running
-- container for work that spans cycles, with progress tracking by state
-- groups, archive, and initiative links. glance had no epic concept;
-- this migration adds it, mirroring the module_issues /
-- initiative_projects junction conventions exactly.
--
-- Storage:
--   epics: project-scoped (project_id ON DELETE CASCADE), UNIQUE on
--     (project_id, name) like modules — a duplicate create is 409.
--     created_by is SET NULL (not CASCADE) so deleting a user never
--     deletes an epic, like initiatives.
--   epic_issues: junction (epic_id, issue_id) PK + FKs cascade — deletes
--     are cheap and the issues themselves are untouched, like
--     module_issues.
--   initiative_epics: junction (initiative_id, epic_id) PK + FKs cascade,
--     like initiative_projects.
--
-- Idempotent: safe to re-run (IF NOT EXISTS everywhere).

CREATE TABLE IF NOT EXISTS epics (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id  UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    description TEXT,
    archived_at TIMESTAMPTZ,
    created_by  UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (project_id, name)
);
CREATE INDEX IF NOT EXISTS epics_project_idx
    ON epics (project_id, archived_at);

CREATE TABLE IF NOT EXISTS epic_issues (
    epic_id    UUID NOT NULL REFERENCES epics(id) ON DELETE CASCADE,
    issue_id   UUID NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (epic_id, issue_id)
);
CREATE INDEX IF NOT EXISTS epic_issues_issue_idx
    ON epic_issues (issue_id);

CREATE TABLE IF NOT EXISTS initiative_epics (
    initiative_id UUID NOT NULL REFERENCES initiatives(id) ON DELETE CASCADE,
    epic_id       UUID NOT NULL REFERENCES epics(id) ON DELETE CASCADE,
    added_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (initiative_id, epic_id)
);
CREATE INDEX IF NOT EXISTS initiative_epics_epic_idx
    ON initiative_epics (epic_id);
