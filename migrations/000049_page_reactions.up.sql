-- C24T3 up: page reactions. The page analogue of issue_reactions
-- (000010): one row per (page, user, emoji) pair, INSERT ON CONFLICT
-- DO NOTHING for idempotent adds. Deleting a page cascades its
-- reactions (same convention as issue_reactions); deleting a user
-- cascades too (issue satellites use CASCADE, so no dangling rows).
-- Idempotent: safe to re-run.
CREATE TABLE IF NOT EXISTS page_reactions (
    page_id    UUID NOT NULL REFERENCES pages(id) ON DELETE CASCADE,
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    emoji      TEXT NOT NULL CHECK (char_length(emoji) BETWEEN 1 AND 32),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (page_id, user_id, emoji)
);
CREATE INDEX IF NOT EXISTS page_reactions_page_idx ON page_reactions (page_id);
