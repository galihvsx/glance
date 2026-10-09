-- 000026_issue_parent down: drop what the up migration added.
-- The parent_id column itself predates this migration (000007) and is
-- left in place; the self-heal UPDATE is not reversed (it only removed
-- invalid self-parents).
DROP INDEX IF EXISTS idx_issues_parent_id;
ALTER TABLE issues DROP CONSTRAINT IF EXISTS issues_parent_no_self;
