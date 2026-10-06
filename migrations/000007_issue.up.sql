-- 000007_issue: issues + issue_sequences + issue_activities (spec §4, Task 14).
--
-- issues.sequence_id is the per-project counter behind display IDs
-- (ENG-123); it is UNIQUE per project as a backstop — the atomic
-- increment itself is an atomic INSERT ... ON CONFLICT DO UPDATE in the
-- service layer (single row lock, gapless under concurrency, self-healing
-- when the counter row is missing).
-- issue_sequences rows are seeded at project creation (service.CreateProject)
-- and backfilled below for projects predating this migration.
-- issues.estimate_point_id has NO FK yet: estimate_points lands in
-- 000008_taxonomy (Task 16), which adds the constraint via ALTER TABLE.
-- issues.search is a generated tsvector (name + description text); the
-- description::text cast is immutable, so the generated column is legal.
CREATE TABLE IF NOT EXISTS issue_sequences (
    project_id UUID PRIMARY KEY REFERENCES projects(id) ON DELETE CASCADE,
    last_value BIGINT NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS issues (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id        UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    sequence_id       INT NOT NULL,
    name              TEXT NOT NULL,
    description       JSONB,
    priority          SMALLINT NOT NULL DEFAULT 0 CHECK (priority BETWEEN 0 AND 4),
    state_id          UUID NOT NULL REFERENCES states(id),
    parent_id         UUID REFERENCES issues(id) ON DELETE SET NULL,
    sort_order        FLOAT8 NOT NULL DEFAULT 0,
    start_date        DATE,
    target_date       DATE,
    estimate_point_id UUID,
    is_draft          BOOL NOT NULL DEFAULT FALSE,
    archived_at       TIMESTAMPTZ,
    deleted_at        TIMESTAMPTZ,
    search            TSVECTOR GENERATED ALWAYS AS (
        to_tsvector('english', coalesce(name, '') || ' ' || coalesce(description::text, ''))
    ) STORED,
    created_by        UUID NOT NULL REFERENCES users(id),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (project_id, sequence_id)
);
-- Lookup: "live issues of a project". Partial index — soft-deleted rows
-- are never listed (Task 15 builds on this).
CREATE INDEX IF NOT EXISTS issues_project_idx ON issues (project_id) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS issues_state_idx ON issues (state_id) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS issues_search_idx ON issues USING GIN (search);

-- issue_activities is append-only (no updated_at, no delete path):
-- field-level audit; field='_created' on insert, one row per changed
-- field on PATCH, field='_deleted' on soft delete.
CREATE TABLE IF NOT EXISTS issue_activities (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    issue_id   UUID NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
    actor_id   UUID NOT NULL REFERENCES users(id),
    field      TEXT NOT NULL,
    old_value  JSONB,
    new_value  JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- Lookup: "history of an issue, oldest first".
CREATE INDEX IF NOT EXISTS issue_activities_issue_idx ON issue_activities (issue_id, created_at);

-- Backfill: projects created before this migration get a counter row so
-- the atomic UPDATE..RETURNING in CreateIssue is unconditional.
INSERT INTO issue_sequences (project_id)
SELECT id FROM projects
ON CONFLICT (project_id) DO NOTHING;
