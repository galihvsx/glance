-- 000066_pages_search: full-text search over pages (C66T0).
--
-- Global search (GET /api/v1/search) was issues-only: pages had a title
-- and content but no search vector. Mirrors issues.search (000007): a
-- generated tsvector with the title weighted 'A' and the content
-- weighted 'B', plus a GIN index. Generated columns backfill
-- automatically on existing rows, so no data migration is needed.
--
-- Idempotent: safe to re-run (ADD COLUMN IF NOT EXISTS / index
-- IF NOT EXISTS).
ALTER TABLE pages ADD COLUMN IF NOT EXISTS search TSVECTOR GENERATED ALWAYS AS (
    setweight(to_tsvector('english', coalesce(title, '')), 'A') ||
    setweight(to_tsvector('english', coalesce(content, '')), 'B')
) STORED;
CREATE INDEX IF NOT EXISTS pages_search_idx ON pages USING GIN (search);
