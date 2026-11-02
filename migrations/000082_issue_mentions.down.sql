-- 000082_issue_mentions down: drop the persisted @-mention index.
-- The index is fully derivable from comment bodies (BackfillIssueMentions
-- rebuilds it), so dropping it loses no source data — only the cached
-- resolution. Re-applying the up migration plus a boot backfill
-- restores it.

DROP TABLE IF EXISTS issue_mentions;
