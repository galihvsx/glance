-- 000064_webhook_delivery_status: last-delivery outcome on the webhook
-- row (C57T2). A webhook that fails all 8 attempts goes silently dead
-- today — the only way to learn about it is to query the DB. These three
-- columns make delivery health visible in the API and the settings UI.
--
-- All three NULLABLE: NULL = never attempted (a new webhook, or one
-- whose enqueued rows were all skipped because it was deleted /
-- deactivated before delivery). The dispatcher writes them alongside
-- each outbox outcome write (service.WebhookDispatcher outcome writes):
--   delivered → last_delivery_at = now(), status 'delivered', last_error NULL
--   retrying  → last_delivery_at = now(), status 'retrying',  last_error = sendErr
--   failed    → last_delivery_at = now(), status 'failed',    last_error = sendErr
-- The status update never touches updated_at: delivery outcomes are
-- operational metadata, not a user edit of the webhook.
--
-- Idempotent: safe to re-run (ADD COLUMN IF NOT EXISTS). Existing rows
-- get NULLs (never attempted), which the CHECK accepts.
ALTER TABLE webhooks
    ADD COLUMN IF NOT EXISTS last_delivery_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS last_delivery_status TEXT
        CHECK (last_delivery_status IN ('delivered', 'retrying', 'failed')),
    ADD COLUMN IF NOT EXISTS last_error TEXT;
