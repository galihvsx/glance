-- 000036_digest_watermarks: daily email-digest idempotency (C10T2).
--
-- digest_watermarks: one row per user per calendar day the digest job has
-- emailed. The digest job claims the row with INSERT ... ON CONFLICT
-- (user_id, digest_date) DO NOTHING — a second pass the same day (or a
-- concurrent pass) claims zero rows and sends nothing, so each user gets
-- at most one digest email per day. The row is written in the same tx as
-- the "email.digest" outbox row, so the watermark and the queued email
-- commit atomically.
--
-- The row is claimed only when an email is actually sent: an empty digest
-- (no digest-worthy events in the last 24h) leaves no row, so events
-- arriving later the same day can still be picked up by a later pass.
-- user_id cascades on user delete, so removing a user drops its digest
-- state with it.
-- IF NOT EXISTS so the migration is re-runnable.
CREATE TABLE IF NOT EXISTS digest_watermarks (
    user_id     UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    digest_date DATE NOT NULL,
    sent_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, digest_date)
);
