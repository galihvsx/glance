-- 000033_issue_views down: drop the issue_views table and its indexes.
DROP INDEX IF EXISTS idx_issue_views_project_user;
DROP INDEX IF EXISTS idx_issue_views_user_project_name;
DROP TABLE IF EXISTS issue_views;
