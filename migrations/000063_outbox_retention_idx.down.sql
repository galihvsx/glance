-- C57T1 down: drop the outbox retention prune index.
-- Idempotent: safe to re-run. Dropping the index does not touch
-- any outbox rows; the prune (service.PruneOutbox) keeps working,
-- it just falls back to a sequential scan.
DROP INDEX IF EXISTS outbox_prune_idx;
