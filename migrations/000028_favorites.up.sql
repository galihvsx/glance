-- 000027_favorites: per-user starred issues and projects (C7T4).
--
-- favorites: one row per (user, favoritable) pair. favoritable_type is
-- one of 'issue' | 'project'; favoritable_id is the target's UUID.
-- No FK to issues/projects on purpose: favorites must survive target
-- visibility checks done at read time, and orphan rows are filtered by
-- the service's JOINs (a deleted issue/project simply drops out of the
-- list instead of breaking a cascade). user_id cascades on user delete.
-- IF NOT EXISTS so the migration is re-runnable.
CREATE TABLE IF NOT EXISTS favorites (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    favoritable_type TEXT NOT NULL CHECK (favoritable_type IN ('issue', 'project')),
    favoritable_id  UUID NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_favorites UNIQUE (user_id, favoritable_type, favoritable_id)
);

CREATE INDEX IF NOT EXISTS idx_favorites_user ON favorites (user_id, created_at DESC);
