-- 000081_issue_views_workspace_scope down: drop the workspace scope.
--
-- Reverses the up migration exactly: the exactly-one-scope CHECK goes,
-- the workspace partial unique + list indexes go, the project partial
-- unique index is replaced by the original plain unique index, the
-- workspace_id column goes, and project_id is NOT NULL again.
--
-- Workspace-scoped rows cannot be restored to project scope (they have
-- no project to restore to), so they are deleted on downgrade — the
-- same shape as 000035's down migration losing sharing state. Project
-- rows survive the round trip unchanged.
ALTER TABLE issue_views
    DROP CONSTRAINT IF EXISTS chk_issue_views_exactly_one_scope;

DROP INDEX IF EXISTS idx_issue_views_user_workspace_name;
DROP INDEX IF EXISTS idx_issue_views_workspace_user;
DROP INDEX IF EXISTS idx_issue_views_user_project_name;

-- The DELETE must not run after the column is gone (a second down
-- run), so it is guarded by a column-existence check with dynamic
-- SQL — a static DELETE would fail to parse once the column is
-- dropped, even inside a conditional block.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = 'public' AND table_name = 'issue_views'
          AND column_name = 'workspace_id'
    ) THEN
        EXECUTE 'DELETE FROM issue_views WHERE workspace_id IS NOT NULL';
    END IF;
END $$;

ALTER TABLE issue_views
    DROP COLUMN IF EXISTS workspace_id;

ALTER TABLE issue_views
    ALTER COLUMN project_id SET NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_issue_views_user_project_name
    ON issue_views (user_id, project_id, lower(name));
