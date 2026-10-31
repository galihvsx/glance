-- 000078_project_acl_lead down: drop the C92T0 ACL objects.
-- The visibility column and the Lead column are dropped with the
-- table; projects and teamspaces themselves are untouched.
DROP TABLE IF EXISTS project_members;
ALTER TABLE projects DROP CONSTRAINT IF EXISTS projects_visibility_check;
ALTER TABLE projects DROP COLUMN IF EXISTS visibility;
ALTER TABLE teamspaces DROP COLUMN IF EXISTS lead_user_id;
