-- 000048_project_archive down: drop the project archived_at column.
-- Idempotent: safe to re-run.
ALTER TABLE projects DROP COLUMN IF EXISTS archived_at;
