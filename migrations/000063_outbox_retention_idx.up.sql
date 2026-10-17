-- 000063_outbox_retention_idx: partial index serving the C57T1
-- outbox retention prune (service.PruneOutbox).
--
-- The prune deletes terminal rows (status IN ('done','failed'))
-- with processed_at older than the retention window. Without this
-- index that predicate scans the whole outbox — which grows
-- unboundedly with every issue mutation (one row per subscribed
-- webhook). The partial index matches the prune predicate exactly,
-- so the planner can serve both the filter and the ORDER BY
-- processed_at from the index.
--
-- Only terminal rows are indexed: pending (undelivered work) and
-- delivering (lease recovery) rows are never pruned and never need
-- this access path; the dispatcher's outbox_due_idx (000002) still
-- serves the claim query. Idempotent: safe to re-run.
CREATE INDEX IF NOT EXISTS outbox_prune_idx
    ON outbox (processed_at)
    WHERE status IN ('done', 'failed');
