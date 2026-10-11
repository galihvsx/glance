-- C18T4: project icons (emoji). The icon column is NULL = unset; the
-- server validates the value on write (single emoji only: 1 grapheme, no
-- ZWJ sequences; empty string is normalized to NULL), so the column
-- carries no CHECK — validation lives in Go where grapheme rules are
-- expressible. Idempotent: safe to re-run.
ALTER TABLE projects ADD COLUMN IF NOT EXISTS icon TEXT NULL;
