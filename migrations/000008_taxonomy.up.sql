-- 000008_taxonomy: labels + assignments + estimates (spec §4, Task 16).
--
-- labels are WORKSPACE-scoped per spec §4 (workspace_id FK), not
-- project-scoped: one label taxonomy shared by every project in the
-- workspace (Linear-style). The API nests label routes under
-- /projects/{identifier}/labels for Plane-style ergonomics and
-- membership resolution, but the rows belong to the workspace.
-- name is unique per workspace (409 on conflict).
-- parent_id is a self-FK for hierarchy; ON DELETE SET NULL so deleting
-- a parent promotes its children to roots instead of cascading.
-- Cycle prevention is enforced in the service layer (a recursive CTE
-- would also work, but the check-then-act needs the row lock anyway).
--
-- issue_labels / issue_assignees are the junction tables Task 15's list
-- query was written against (its FALSE stubs and '[]'::jsonb literals
-- are upgraded to real EXISTS filters and json_agg subqueries now that
-- the tables exist).
--
-- estimates are per-project scales (e.g. "Fibonacci"); estimate_points
-- are the selectable values. issues.estimate_point_id was added without
-- its FK in 000007; the constraint lands here via ALTER TABLE, and
-- point deletion SETs NULL rather than nuking issues.
CREATE TABLE IF NOT EXISTS labels (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    parent_id    UUID REFERENCES labels(id) ON DELETE SET NULL,
    name         TEXT NOT NULL,
    color        TEXT NOT NULL DEFAULT '#6b7280',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, name)
);
CREATE INDEX IF NOT EXISTS labels_workspace_idx ON labels (workspace_id);
CREATE INDEX IF NOT EXISTS labels_parent_idx ON labels (parent_id) WHERE parent_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS issue_labels (
    issue_id UUID NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
    label_id UUID NOT NULL REFERENCES labels(id) ON DELETE CASCADE,
    PRIMARY KEY (issue_id, label_id)
);
CREATE INDEX IF NOT EXISTS issue_labels_label_idx ON issue_labels (label_id);

CREATE TABLE IF NOT EXISTS issue_assignees (
    issue_id UUID NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
    user_id  UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    PRIMARY KEY (issue_id, user_id)
);
CREATE INDEX IF NOT EXISTS issue_assignees_user_idx ON issue_assignees (user_id);

CREATE TABLE IF NOT EXISTS estimates (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (project_id, name)
);
CREATE INDEX IF NOT EXISTS estimates_project_idx ON estimates (project_id);

CREATE TABLE IF NOT EXISTS estimate_points (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    estimate_id UUID NOT NULL REFERENCES estimates(id) ON DELETE CASCADE,
    key         TEXT NOT NULL,
    value       INT NOT NULL,
    description TEXT,
    UNIQUE (estimate_id, key)
);
CREATE INDEX IF NOT EXISTS estimate_points_estimate_idx ON estimate_points (estimate_id);

-- Task 14 carry-over: the FK for issues.estimate_point_id, deferred from
-- 000007 because estimate_points did not exist yet. The column itself was
-- created in 000007; only the constraint is added here.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'issues_estimate_point_id_fkey'
    ) THEN
        ALTER TABLE issues
            ADD CONSTRAINT issues_estimate_point_id_fkey
            FOREIGN KEY (estimate_point_id) REFERENCES estimate_points(id)
            ON DELETE SET NULL;
    END IF;
END $$;
