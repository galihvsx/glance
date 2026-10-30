-- 000077_teamspace_members: per-teamspace membership + visibility (C90T0).
--
-- Cycle 89 shipped teamspaces as grouping + navigation only, visible to
-- every workspace member. This migration adds the membership table that
-- gates visibility (Plane parity: workspace members see only the
-- teamspaces they belong to; workspace Admins see every teamspace).
--
-- Shape follows the task spec, audited against the module_members
-- precedent (000012: FKs ON DELETE CASCADE, created_at default now()):
-- module_members uses a composite PK (module_id, user_id) and no
-- surrogate id, but teamspaces need a stable row id for the members
-- API surface, so this table carries an id UUID PK plus the same
-- UNIQUE(teamspace_id, user_id) pair constraint. The (user_id) index
-- serves the visibility join (list teamspaces where the actor holds a
-- membership row); the UNIQUE index's (teamspace_id, ...) prefix
-- serves the per-teamspace roster lookup, so no second index is added.
--
-- BACKFILL (load-bearing, non-breaking): one membership row per
-- (existing teamspace x current workspace member), so every existing
-- workspace keeps exactly its current visibility — nothing disappears
-- from any sidebar until an Admin removes someone. ON CONFLICT DO
-- NOTHING makes the backfill safe to re-run.
--
-- Deleting a teamspace removes its memberships (ON DELETE CASCADE) and
-- never touches projects; deleting a user removes theirs likewise.
--
-- Idempotent: safe to re-run (IF NOT EXISTS everywhere).
CREATE TABLE IF NOT EXISTS teamspace_members (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    teamspace_id UUID NOT NULL REFERENCES teamspaces(id) ON DELETE CASCADE,
    user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (teamspace_id, user_id)
);
CREATE INDEX IF NOT EXISTS teamspace_members_user_idx
    ON teamspace_members (user_id);

INSERT INTO teamspace_members (id, teamspace_id, user_id)
SELECT gen_random_uuid(), t.id, m.user_id
FROM teamspaces t
JOIN workspace_members m ON m.workspace_id = t.workspace_id
ON CONFLICT DO NOTHING;
