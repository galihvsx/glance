-- 000004_session_last_seen down: drop what the up migration added.
DROP INDEX IF EXISTS sessions_token_hash_idx;
ALTER TABLE sessions DROP COLUMN IF EXISTS last_seen_at;
