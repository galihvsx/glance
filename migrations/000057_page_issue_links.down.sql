-- 000057_page_issue_links down: drop the page<->issue link table.
-- Idempotent: safe to re-run.
DROP TABLE IF EXISTS page_issue_links;
