-- C32T0 down: drop the comment-resolution columns.
ALTER TABLE comments DROP COLUMN IF EXISTS resolved_by,
    DROP COLUMN IF EXISTS resolved_at;
