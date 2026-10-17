-- C47T1 down: drop the link-time column from key_result_issues.
-- Idempotent: safe to re-run. The backfilled values are not
-- recoverable after this (they were a lower bound, not the true link
-- time) — re-applying the up migration recomputes them.
ALTER TABLE key_result_issues DROP COLUMN IF EXISTS created_at;
