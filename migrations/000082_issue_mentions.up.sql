-- 000082_issue_mentions: persisted @-mention index (C120T0).
--
-- Mentions today exist ONLY as comment-time notification resolution
-- (internal/service/mention.go: @-tokens are parsed on comment create
-- / update and a `mention` notification is emitted). There is no
-- persisted index, so the Mentions list filter dimension cannot be
-- answered without re-parsing every comment body per query. This
-- migration adds the index table the filter reads.
--
-- Grain is (issue, user, comment) — NOT (issue, user). A comment
-- edit or delete touches only that comment's rows, never a
-- cross-comment recount: if comment A and comment B both mention
-- user X, deleting A must not un-mention X, and with per-comment
-- rows it cannot. All three FKs cascade, so deleting an issue, a
-- user, or a comment removes its rows with it — an issue whose only
-- mentions were in since-deleted comments has no rows and therefore
-- matches the `none` sentinel (NOT EXISTS), consistently.
--
-- Write path (internal/service/issue_mentions.go): comment create /
-- update delete-then-insert that comment's rows in the SAME tx as
-- the comment write; DeleteComment deletes them in the same tx as
-- the soft delete. Pre-existing comments are backfilled once at boot
-- by BackfillIssueMentions (single-tx sweep, ON CONFLICT DO NOTHING,
-- skipped when the table is non-empty). Import paths deliberately do
-- NOT populate the index — they follow the notification precedent
-- ("must not fire mention parsing"), so an imported comment
-- mentioning someone matches the filter only after that comment is
-- edited through the normal write path.
--
-- Idempotent: safe to re-run (IF NOT EXISTS everywhere).

CREATE TABLE IF NOT EXISTS issue_mentions (
    issue_id   UUID NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    comment_id UUID NOT NULL REFERENCES comments(id) ON DELETE CASCADE,
    PRIMARY KEY (issue_id, user_id, comment_id)
);

-- The filter path: EXISTS over (issue_id, user_id).
CREATE INDEX IF NOT EXISTS idx_issue_mentions_issue_user
    ON issue_mentions (issue_id, user_id);

-- Per-user lookup (e.g. "which issues mention me").
CREATE INDEX IF NOT EXISTS idx_issue_mentions_user
    ON issue_mentions (user_id);
