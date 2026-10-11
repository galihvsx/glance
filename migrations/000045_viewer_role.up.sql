-- C17T5: viewer role (10, read-only). Widen the workspace_members role
-- CHECK from (5, 15, 20) to (5, 10, 15, 20) so members can hold the new
-- read-only viewer level. No data change: existing rows are untouched.
-- Enforcement lives in Go (writes gate on role >= RoleMember = 15;
-- reads only require membership), so the constraint is the only schema
-- piece the new level needs.
ALTER TABLE workspace_members
    DROP CONSTRAINT IF EXISTS workspace_members_role_check;
ALTER TABLE workspace_members
    ADD CONSTRAINT workspace_members_role_check CHECK (role IN (5, 10, 15, 20));
