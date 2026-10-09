-- 000035_issue_views_shared down: drop the shared flag. Sharing state is
-- lost on downgrade (there is nothing to back it up to — the column is
-- the whole feature).
ALTER TABLE issue_views
    DROP COLUMN IF EXISTS shared;
