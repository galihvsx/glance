-- 000076_teamspaces down: drop the teamspace surface (manual
-- rollback only; the migrator never applies down files).
DROP INDEX IF EXISTS projects_teamspace_idx;
ALTER TABLE projects DROP COLUMN IF EXISTS teamspace_id;
DROP INDEX IF EXISTS teamspaces_workspace_idx;
DROP TABLE IF EXISTS teamspaces;
