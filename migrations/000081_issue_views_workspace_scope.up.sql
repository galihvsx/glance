-- 000081_issue_views_workspace_scope: workspace-scoped saved views (C113T0).
--
-- MyWork (/w/:slug/my-work) needs a Views menu, but issue_views
-- (000033, sharing added 000035) carries a NOT NULL project_id, so
-- every row is project-scoped. This migration adds the second scope:
-- workspace_id NULLABLE with ON DELETE CASCADE, relaxes project_id
-- to NULLABLE, and constrains every row to exactly one scope with a
-- CHECK ((project_id IS NULL) <> (workspace_id IS NULL)) — never both,
-- never neither.
--
-- Name uniqueness cannot stay a single plain unique index: Postgres
-- treats NULLs as distinct, so (user_id, workspace_id, lower(name))
-- over nullable columns would NOT reject two same-named workspace
-- views of one user. The single index is therefore replaced by two
-- partial unique indexes, one per scope, preserving the existing
-- project-scope semantics exactly and adding the workspace-scope
-- equivalent. A (workspace_id, user_id) list index mirrors the
-- existing (project_id, user_id) list index.
--
-- Existing project rows are untouched: they already satisfy the CHECK
-- (project_id set, workspace_id NULL) and the partial project index
-- covers exactly the rows the old index covered.
ALTER TABLE issue_views
    ADD COLUMN IF NOT EXISTS workspace_id UUID REFERENCES workspaces (id) ON DELETE CASCADE;

ALTER TABLE issue_views
    ALTER COLUMN project_id DROP NOT NULL;

DROP INDEX IF EXISTS idx_issue_views_user_project_name;

CREATE UNIQUE INDEX IF NOT EXISTS idx_issue_views_user_project_name
    ON issue_views (user_id, project_id, lower(name))
    WHERE project_id IS NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_issue_views_user_workspace_name
    ON issue_views (user_id, workspace_id, lower(name))
    WHERE workspace_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_issue_views_workspace_user
    ON issue_views (workspace_id, user_id);

-- ADD CONSTRAINT has no IF NOT EXISTS, so guard it: the migrator never
-- re-applies an applied migration, but a manual re-run must not fail.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'chk_issue_views_exactly_one_scope'
    ) THEN
        ALTER TABLE issue_views
            ADD CONSTRAINT chk_issue_views_exactly_one_scope
            CHECK ((project_id IS NULL) <> (workspace_id IS NULL));
    END IF;
END $$;
