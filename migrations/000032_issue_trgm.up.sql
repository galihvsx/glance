-- 000032_issue_trgm: pg_trgm extension + trigram GIN index on issues(name)
-- (C8T7 duplicate detection on issue create).
--
-- NOTE: plain CREATE INDEX, NOT CREATE INDEX CONCURRENTLY — the migrator
-- applies every migration inside a transaction (see internal/store
-- migrate.go), and CONCURRENTLY is illegal inside a transaction block.
-- The build takes a brief lock on issues; acceptable at migration time.
-- IF NOT EXISTS keeps the migration idempotent / re-runnable.
--
-- The index backs the C8T7 "similar issues" lookup:
--   WHERE i.name % $1 ORDER BY similarity(i.name, $1) DESC
-- gin_trgm_ops is the operator class that makes the % (similarity)
-- operator indexable.
CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE INDEX IF NOT EXISTS idx_issues_name_trgm
    ON issues USING gin (name gin_trgm_ops);
