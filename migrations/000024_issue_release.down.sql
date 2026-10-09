-- 000024_issue_release down: drop what the up migration added.
-- Dropping the column also drops its FK constraint.
ALTER TABLE issues DROP COLUMN IF EXISTS release_id;
