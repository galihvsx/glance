-- C15T3: per-user iCal subscription feed tokens.
--
-- A feed token authenticates the ?token= credential on the calendar.ics
-- subscription feeds (external calendar apps cannot present the session
-- cookie). Like api_tokens, only the SHA-256 hash of the secret is
-- stored; the plaintext (glcal_…) is shown exactly once in the POST
-- response and never again. One live token per user at a time (the
-- partial unique index); POST /api/v1/me/calendar-token revokes the
-- previous live token before minting the new one (regenerate semantics).
CREATE TABLE IF NOT EXISTS calendar_feed_tokens (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash   TEXT NOT NULL UNIQUE,
    last_used_at TIMESTAMPTZ,
    revoked_at   TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_calendar_feed_tokens_live_user
    ON calendar_feed_tokens(user_id) WHERE revoked_at IS NULL;
