-- C18T2 up: page comments. A flat (non-threaded) comment list per
-- wiki page, oldest first. Body is a TipTap JSON doc (JSONB), validated
-- exactly like issue comment content: whitespace-trimmed, non-empty.
-- author_id is nullable with ON DELETE SET NULL (plan spec): deleting a
-- user keeps their page comments, attributed to a null author.
-- Idempotent: safe to re-run.
CREATE TABLE IF NOT EXISTS page_comments (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    page_id    UUID NOT NULL REFERENCES pages(id) ON DELETE CASCADE,
    author_id  UUID REFERENCES users(id) ON DELETE SET NULL,
    body       JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS page_comments_page_idx ON page_comments (page_id);
