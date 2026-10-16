-- 000054_comment_resolve: per-thread-root resolution state on comments
-- (cycle 32 "Comment resolve threads", C32T0).
--
-- resolved_at / resolved_by mark a thread root resolved (who + when);
-- replies always carry NULLs — resolution is a root-only property, and
-- the service rejects resolve/reopen on a reply with 400. resolved_by
-- references users(id) ON DELETE SET NULL: deleting a user keeps the
-- thread resolved (resolved_at intact) but drops the resolver identity,
-- same posture as the actor FKs elsewhere. No index needed: resolution
-- is read with the comment row, never a standalone lookup.
-- Idempotent: safe to re-run.
ALTER TABLE comments ADD COLUMN IF NOT EXISTS resolved_at timestamptz,
    ADD COLUMN IF NOT EXISTS resolved_by uuid REFERENCES users(id) ON DELETE SET NULL;
