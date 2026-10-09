-- 000033_issue_views: per-user saved views (C9T2 backend persistence).
--
-- issue_views: named filter+display presets a user saves on a project.
-- filters stores the IssueFilters shape (web/src/lib/filters.ts) opaquely
-- in JSONB — the backend never interprets it, it round-trips verbatim.
-- display stores the DisplaySettings object from useDisplaySettings and
-- may be NULL when the view was saved from a page without display
-- settings (spreadsheet). is_default marks the one default view per
-- (user, project); the service clears sibling defaults in the same
-- transaction when a new default is set, so no partial unique index is
-- needed.
--
-- Name uniqueness is case-insensitive per (user_id, project_id) —
-- lower(name) — to match the frontend's duplicate-name check. Rows die
-- with the project (ON DELETE CASCADE) and with the user.
-- IF NOT EXISTS keeps the migration idempotent / re-runnable.
CREATE TABLE IF NOT EXISTS issue_views (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    project_id  UUID NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    filters     JSONB NOT NULL,
    display     JSONB,
    is_default  BOOLEAN NOT NULL DEFAULT FALSE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_issue_views_user_project_name
    ON issue_views (user_id, project_id, lower(name));

CREATE INDEX IF NOT EXISTS idx_issue_views_project_user
    ON issue_views (project_id, user_id);
