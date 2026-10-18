-- 000068_webhook_filter down: drop the per-webhook event filter column.
ALTER TABLE webhooks DROP COLUMN IF EXISTS filter;
