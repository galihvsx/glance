-- 000065_webhook_deliveries: per-attempt delivery history (C59T0).
-- Cycles 56–58 made fan-out complete, delivery reliable + observable
-- (last-delivery summary), and dispatch fair — but the only per-webhook
-- delivery state was the C57T2 summary (a single latest-outcome row on
-- the webhook itself). The outbox is not history: attempts/last_error
-- are overwritten on every retry and terminal rows are pruned after
-- 30 days (C57T1). This table is the per-attempt log: the dispatcher
-- writes one row per attempt (delivered / retrying / failed) alongside
-- each outbox outcome, and redelivery reads event + data back from it.
--
-- Columns:
--   webhook_id    — owner. ON DELETE CASCADE: deleting a webhook
--                   deletes its history (same ownership rule as the
--                   webhook itself; history is admin-only data about
--                   that webhook).
--   outbox_row_id — correlation to the originating outbox row.
--                   NULLABLE and deliberately NOT a foreign key:
--                   outbox rows are pruned after 30d (C57T1) and must
--                   never be blocked by history.
--   event         — the domain event (e.g. issue.updated), copied
--                   from the outbox payload at attempt time.
--   data          — the event payload data (webhookDelivery.Data),
--                   stored so redelivery works independently of
--                   outbox retention. Domain event data ONLY: it
--                   never contains the URL or the signing secret
--                   (those live on the webhook row and are re-read
--                   at redelivery time).
--   status        — delivered | retrying | failed, the same
--                   vocabulary as C57T2's last_delivery_status.
--   http_status   — the receiver's HTTP status for this attempt;
--                   NULL when no HTTP response was received (DNS
--                   failure, dial error, timeout, SSRF refusal,
--                   poison row).
--   error         — the failure message, same string the outbox
--                   last_error carries; NULL on delivered.
--   duration_ms   — attempt wall time, milliseconds, measured
--                   around the HTTP attempt.
--   attempted_at  — when the attempt happened.
--
-- Idempotent: safe to re-run (CREATE TABLE IF NOT EXISTS / index
-- IF NOT EXISTS).
CREATE TABLE IF NOT EXISTS webhook_deliveries (
    id            BIGSERIAL PRIMARY KEY,
    webhook_id    UUID NOT NULL REFERENCES webhooks (id) ON DELETE CASCADE,
    outbox_row_id BIGINT,
    event         TEXT NOT NULL,
    data          JSONB NOT NULL DEFAULT '{}',
    status        TEXT NOT NULL
        CHECK (status IN ('delivered', 'retrying', 'failed')),
    http_status   INT,
    error         TEXT,
    duration_ms   INT NOT NULL DEFAULT 0,
    attempted_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS webhook_deliveries_webhook_idx
    ON webhook_deliveries (webhook_id, attempted_at DESC, id DESC);
