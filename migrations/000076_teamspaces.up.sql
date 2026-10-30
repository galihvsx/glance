-- 000076_teamspaces: teamspaces (C89T0) — workspace-scoped groups
-- that organize projects in the sidebar (Plane "teamspaces" parity).
--
-- teamspaces are grouping + navigation ONLY: no per-teamspace
-- membership/ACL (glance's role model is workspace-level), visible to
-- every workspace member. The workspace FK cascades (matches the
-- initiatives / project_labels precedent); the projects FK is
-- ON DELETE SET NULL — deleting a teamspace must NEVER delete
-- projects, it only ungroups them (the load-bearing invariant).
--
-- Name uniqueness is plain UNIQUE (workspace_id, name), matching the
-- initiatives (000050) and project_labels (000070) precedent — the
-- codebase has no case-insensitive name-uniqueness precedent, so none
-- is invented here.
--
-- Idempotent: safe to re-run (IF NOT EXISTS everywhere).
CREATE TABLE IF NOT EXISTS teamspaces (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, name)
);
CREATE INDEX IF NOT EXISTS teamspaces_workspace_idx
    ON teamspaces (workspace_id, name);

ALTER TABLE projects
    ADD COLUMN IF NOT EXISTS teamspace_id UUID REFERENCES teamspaces(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS projects_teamspace_idx
    ON projects (teamspace_id);
