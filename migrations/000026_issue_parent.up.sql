-- 000026_issue_parent: sub-issue backstops on issues.parent_id (C5T4).
--
-- The parent relation column itself already exists (migration 000007,
-- parent_id UUID REFERENCES issues(id) ON DELETE SET NULL — which also
-- gives us the "deleting a parent leaves children as top-level issues"
-- behavior: ON DELETE SET NULL). This migration adds the two pieces the
-- new SetParent service contract needs at the DB level:
--
--   1. A self-parent CHECK (parent_id <> id): the service rejects
--      self-parents with 409 first, but the CHECK is the race/ORM-bypass
--      backstop.
--   2. An index on parent_id: ?include_children=1 on the issue detail
--      endpoint filters on parent_id, and the cycle guard walks the
--      ancestor chain one hop at a time.
--
-- Existing rows are self-healed first (defensive: the old PATCH contract
-- allowed a self-parent, so a stray row would abort ADD CONSTRAINT).
-- IF NOT EXISTS so the migration is re-runnable.
UPDATE issues SET parent_id = NULL WHERE parent_id IS NOT NULL AND parent_id = id;
ALTER TABLE issues DROP CONSTRAINT IF EXISTS issues_parent_no_self;
ALTER TABLE issues
    ADD CONSTRAINT issues_parent_no_self CHECK (parent_id <> id);
CREATE INDEX IF NOT EXISTS idx_issues_parent_id ON issues(parent_id);
