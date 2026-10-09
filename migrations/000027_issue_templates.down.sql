-- 000027_issue_templates down: drop what the up migration added.
-- Deleting a project cascades its templates, so dropping the table is the
-- full reversal; nothing else references it.
DROP TABLE IF EXISTS issue_templates;
