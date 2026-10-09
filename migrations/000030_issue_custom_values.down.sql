-- 000030_issue_custom_values down: drop the issue_custom_values table and
-- its index.
DROP INDEX IF EXISTS idx_issue_custom_values_issue;
DROP TABLE IF EXISTS issue_custom_values;
