-- 000023_releases: project releases/milestones (C4T6).
--
-- releases: project-scoped milestone rows. status is planned|released
-- (CHECK). UNIQUE(project_id, name). Issues attach via issues.release_id
-- (000024, nullable FK with ON DELETE SET NULL). Everything is
-- IF NOT EXISTS so the migration is re-runnable.
CREATE TABLE IF NOT EXISTS releases (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id   UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    description  TEXT NOT NULL DEFAULT '',
    status       TEXT NOT NULL DEFAULT 'planned' CHECK (status IN ('planned', 'released')),
    release_date DATE,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (project_id, name)
);
CREATE INDEX IF NOT EXISTS idx_releases_project
    ON releases(project_id, created_at DESC);
