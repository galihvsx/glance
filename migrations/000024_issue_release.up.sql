-- 000024_issue_release: issues gain an optional release assignment (C4T6).
--
-- release_id is a nullable FK to releases with ON DELETE SET NULL:
-- deleting a release detaches its issues instead of deleting them.
-- IF NOT EXISTS so the migration is re-runnable.
ALTER TABLE issues
    ADD COLUMN IF NOT EXISTS release_id UUID REFERENCES releases(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS idx_issues_release ON issues(release_id);
