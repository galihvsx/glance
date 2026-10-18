-- C57T2 down: drop the last-delivery outcome columns from webhooks.
-- Idempotent: safe to re-run. Dropping the columns loses delivery
-- history (by design — down migrations are for rollback, not
-- archiving); the dispatcher keeps working, it just stops recording
-- outcomes.
ALTER TABLE webhooks
    DROP COLUMN IF EXISTS last_delivery_at,
    DROP COLUMN IF EXISTS last_delivery_status,
    DROP COLUMN IF EXISTS last_error;
