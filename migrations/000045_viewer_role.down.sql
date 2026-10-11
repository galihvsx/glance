-- C17T5 down: revert the workspace_members role CHECK to (5, 15, 20).
-- Fails if any viewer (role 10) rows exist — demote or remove them
-- before rolling back.
ALTER TABLE workspace_members
    DROP CONSTRAINT IF EXISTS workspace_members_role_check;
ALTER TABLE workspace_members
    ADD CONSTRAINT workspace_members_role_check CHECK (role IN (5, 15, 20));
