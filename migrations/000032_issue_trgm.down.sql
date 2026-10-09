-- 000032_issue_trgm down: drop the trigram index. The pg_trgm extension
-- itself is left installed — extensions are cheap and shared, and later
-- consumers may rely on it.
DROP INDEX IF EXISTS idx_issues_name_trgm;
