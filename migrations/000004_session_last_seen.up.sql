-- 000004_session_last_seen: session activity tracking (Task 9).
--
-- last_seen_at powers the "last active" column in settings → sessions.
-- RequireAuth refreshes it, but only when it is older than 5 minutes, so
-- hot requests skip the write (see auth.AuthenticateSession).
--
-- The original auth migration indexed sessions(user_id) only; the
-- middleware looks sessions up by token_hash on EVERY authenticated
-- request, so that column needs its own index.
ALTER TABLE sessions
    ADD COLUMN IF NOT EXISTS last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now();

CREATE INDEX IF NOT EXISTS sessions_token_hash_idx ON sessions (token_hash);
