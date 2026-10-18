-- C59T0 down: drop the webhook_deliveries history table.
-- Idempotent: safe to re-run. Dropping the table loses delivery
-- history (by design — down migrations are for rollback, not
-- archiving); a rollback pairs with the C59T0 code (the dispatcher
-- inserts into this table, so dropping it without rolling the code
-- back would fail history writes).
DROP TABLE IF EXISTS webhook_deliveries;
