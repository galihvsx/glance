-- 000016_perf_indexes: composite index for the hot issue-list path (Task 27).
--
-- The default list order (-updated_at, the order every list call uses
-- unless order_by says otherwise) previously resolved as a bitmap scan
-- on issues_project_idx plus a top-N heapsort over EVERY live issue in
-- the project: O(n log n) in project size (EXPLAIN ANALYZE in
-- docs/perf.md). This partial composite index serves the filter AND the
-- order straight from the index — no sort step — so list latency stays
-- O(per_page) as projects grow past tens of thousands of issues.
--
-- The partial predicate mirrors issues_project_idx (deleted_at IS NULL):
-- soft-deleted rows never enter the index, and the list query's
-- `i.deleted_at IS NULL` cond matches the predicate exactly.
--
-- Only the default order gets a composite: the other listOrders
-- (created_at, sequence_id, sort_order, priority) keep the
-- bitmap-scan-plus-sort plan, which is fine at realistic project sizes
-- and avoids write amplification on the hot issues table (updated_at
-- changes on every issue update). Add more when a workload proves need.
CREATE INDEX IF NOT EXISTS issues_list_default_idx
    ON issues (project_id, updated_at DESC, id DESC)
    WHERE deleted_at IS NULL;
