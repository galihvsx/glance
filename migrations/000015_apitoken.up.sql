-- Task 29: API tokens for programmatic access (spec §4).
--
-- Only the SHA-256 hash of a token is stored; the plaintext (gl_…) is
-- shown once in the create response and never again. Scopes follow the
-- minimal vocabulary: read, write (write implies read). rate_limit NULL
-- means the server default budget (600 req/min); a per-token override is
-- reserved for future use.
CREATE TABLE IF NOT EXISTS api_tokens (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    token_hash   TEXT NOT NULL UNIQUE,
    scopes       TEXT[] NOT NULL DEFAULT '{read}',
    rate_limit   INT,
    expires_at   TIMESTAMPTZ,
    last_used_at TIMESTAMPTZ,
    revoked_at   TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_api_tokens_user
    ON api_tokens(user_id) WHERE revoked_at IS NULL;
