-- 000008_taxonomy down: drop in reverse dependency order, then the FK
-- added to issues by the up migration.
ALTER TABLE issues DROP CONSTRAINT IF EXISTS issues_estimate_point_id_fkey;
DROP TABLE IF EXISTS estimate_points;
DROP TABLE IF EXISTS estimates;
DROP TABLE IF EXISTS issue_assignees;
DROP TABLE IF EXISTS issue_labels;
DROP TABLE IF EXISTS labels;
