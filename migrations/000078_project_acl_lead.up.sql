-- 000078_project_acl_lead: project ACL via teamspace membership +
-- teamspace Lead designation (C92T0, Plane parity).
--
-- projects.visibility: 'public' | 'private'. Backfill: every existing
-- project -> 'public', so today's visibility is preserved exactly
-- (non-breaking, same pattern as the C90 teamspace_members backfill).
-- The CHECK constraint pins the two legal values; the column default
-- keeps new projects public unless explicitly made private.
--
-- project_members: direct per-project membership mirroring the
-- workspace ladder (admin 20 / member 15 / guest 5). Composite PK
-- (project_id, user_id) is the UNIQUE pair — the module_members
-- precedent (000012) — no surrogate id: the members API addresses
-- rows by (project, user), never by row id. FKs ON DELETE CASCADE:
-- deleting a project or a user removes their memberships.
--
-- teamspaces.lead_user_id: nullable Lead designation (one per
-- teamspace). FK to users ON DELETE SET NULL: deleting the Lead user
-- clears the designation rather than blocking the delete. Clearing on
-- teamspace-membership removal is enforced in the service layer
-- (RemoveTeamspaceMember, same transaction) — a CHECK/FK cannot
-- express "must be a member", so the write path owns that invariant,
-- as documented in teamspace.go.
--
-- Idempotent: safe to re-run (IF NOT EXISTS everywhere; the
-- constraint add is guarded by a DO block so a second apply is a
-- no-op rather than a duplicate-constraint error).
CREATE TABLE IF NOT EXISTS project_members (
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role       INT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (project_id, user_id)
);
CREATE INDEX IF NOT EXISTS project_members_user_idx
    ON project_members (user_id);

ALTER TABLE projects
    ADD COLUMN IF NOT EXISTS visibility TEXT NOT NULL DEFAULT 'public';
UPDATE projects SET visibility = 'public' WHERE visibility IS NULL OR visibility NOT IN ('public', 'private');
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'projects_visibility_check'
    ) THEN
        ALTER TABLE projects
            ADD CONSTRAINT projects_visibility_check
            CHECK (visibility IN ('public', 'private'));
    END IF;
END $$;

ALTER TABLE teamspaces
    ADD COLUMN IF NOT EXISTS lead_user_id UUID REFERENCES users(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS teamspaces_lead_idx
    ON teamspaces (lead_user_id);
