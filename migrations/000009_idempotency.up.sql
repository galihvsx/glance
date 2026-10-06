-- 000009_idempotency: idempotency_keys for safe client retries (spec §5, Task 17).
--
-- The Idempotency-Key header is honored on POST mutations (create issue,
-- bulk-update, bulk-delete). Flow: INSERT ... ON CONFLICT DO NOTHING first;
-- the winner executes and stores (status, response_code, response_body);
-- a replay returns the stored response byte-identical without re-executing;
-- a loser racing an in-flight winner (status = 'in_progress') gets 409.
--
-- Scope is (user_id, idem_key): different users' keys never collide.
-- endpoint records where the key was first used (observability only).
-- 24h TTL: expired rows are deleted opportunistically on access (no cron
-- in v1); created_at is indexed for that sweep.
CREATE TABLE IF NOT EXISTS idempotency_keys (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    idem_key      TEXT NOT NULL,
    endpoint      TEXT NOT NULL,
    status        TEXT NOT NULL DEFAULT 'in_progress'
                  CHECK (status IN ('in_progress', 'completed')),
    response_code INT,
    -- TEXT, not JSONB: the replay must be byte-identical to the original
    -- response, and JSONB would re-serialize (key order, spacing).
    response_body TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, idem_key)
);
CREATE INDEX IF NOT EXISTS idx_idempotency_keys_created_at
    ON idempotency_keys (created_at);
