-- 000002_outbox: transactional outbox for async side effects (email, webhooks).
--
-- Producers enqueue inside their own transaction via mail.Enqueue; the
-- in-process dispatcher claims due rows (FOR UPDATE SKIP LOCKED) and delivers
-- them. One table, namespaced events (email.*, webhook.*) — see spec §4.
CREATE TABLE IF NOT EXISTS outbox (
    id            BIGSERIAL PRIMARY KEY,
    event         TEXT NOT NULL,
    payload       JSONB NOT NULL DEFAULT '{}'::jsonb,
    status        TEXT NOT NULL DEFAULT 'pending'
                  CHECK (status IN ('pending', 'done', 'failed')),
    attempts      INT NOT NULL DEFAULT 0,
    next_retry_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_error    TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    processed_at  TIMESTAMPTZ
);

-- The dispatcher's claim query filters on exactly this shape.
CREATE INDEX IF NOT EXISTS outbox_due_idx
    ON outbox (next_retry_at, id)
    WHERE status = 'pending';
