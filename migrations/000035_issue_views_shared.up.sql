-- 000035_issue_views_shared: shared (project-visible) saved views (C10T1).
--
-- Adds a sharing flag to issue_views. A view with shared=true is visible
-- to every member of the project (not just its owner), annotated with its
-- owner in the list response; shared=false rows remain strictly private.
-- Sharing is an explicit per-view opt-in (default false); the
-- (user_id, project_id, lower(name)) uniqueness is unchanged, so two
-- users may each share a same-named view without conflict.
--
-- IF NOT EXISTS keeps the migration idempotent / re-runnable. Existing
-- rows take the DEFAULT (false) — no backfill needed.
ALTER TABLE issue_views
    ADD COLUMN IF NOT EXISTS shared BOOLEAN NOT NULL DEFAULT FALSE;
