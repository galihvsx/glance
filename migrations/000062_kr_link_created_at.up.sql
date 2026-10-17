-- C47T1 up: link time on key_result_issues. The replay in
-- buildObjectiveProgressHistory needs to know when an issue joined a
-- KR: an issue contributes to a KR's progress only from its link
-- onward, so without a link timestamp the replay would backdate the
-- issue's contribution to before it was linked (progress
-- over-claimed in the past).
--
-- The column is added NULLABLE first, backfilled, then given its
-- DEFAULT + NOT NULL: that keeps re-running this file a no-op for
-- data (the backfill touches only rows whose created_at is still
-- NULL, so a second application never resets a real link time —
-- the 000061 idempotency precedent).
--
-- Backfill semantics — a LOWER BOUND, not the true link time: a link
-- cannot predate either endpoint, so GREATEST(kr.created_at,
-- i.created_at) is the earliest the link could possibly have
-- happened. Pre-migration links may have joined later, so their
-- pre-backfill history is approximate (contribution starts no later
-- than the truth; never later). New links get the true time from
-- DEFAULT now() at insert (LinkIssueToKeyResult needs no SQL change).
ALTER TABLE key_result_issues ADD COLUMN IF NOT EXISTS created_at TIMESTAMPTZ;
UPDATE key_result_issues kri
   SET created_at = GREATEST(kr.created_at, i.created_at)
  FROM key_results kr, issues i
 WHERE kri.key_result_id = kr.id
   AND kri.issue_id = i.id
   AND kri.created_at IS NULL;
ALTER TABLE key_result_issues ALTER COLUMN created_at SET DEFAULT now();
ALTER TABLE key_result_issues ALTER COLUMN created_at SET NOT NULL;
