-- C66T0 down: drop the pages search column and its GIN index.
-- Idempotent: safe to re-run. Dropping the generated column loses no
-- stored data (title/content remain); a rollback pairs with the C66T0
-- code (SearchPages reads the column, so dropping it without rolling
-- the code back would fail search queries).
DROP INDEX IF EXISTS pages_search_idx;
ALTER TABLE pages DROP COLUMN IF EXISTS search;
