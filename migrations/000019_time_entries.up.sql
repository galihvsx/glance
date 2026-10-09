-- 000019_time_entries: issue-scoped worklogs (C3T7).
--
-- time_entries: one row per logged work chunk. ended_at NULL = the
-- user's timer is currently running on this issue.
--
-- The partial unique index uq_time_entries_running enforces "one running
-- timer per user+issue" at the DB level, so concurrent double-starts
-- serialize into a conflict instead of two running rows (the service maps
-- the conflict to 409). The CHECK keeps manual entries honest
-- (ended_at must be after started_at).
--
-- Everything is IF NOT EXISTS so the migration is re-runnable.
CREATE TABLE IF NOT EXISTS time_entries (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    issue_id    UUID NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    started_at  TIMESTAMPTZ NOT NULL,
    ended_at    TIMESTAMPTZ,
    note        TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (ended_at IS NULL OR ended_at > started_at)
);
CREATE INDEX IF NOT EXISTS idx_time_entries_issue
    ON time_entries(issue_id, started_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS uq_time_entries_running
    ON time_entries(issue_id, user_id) WHERE ended_at IS NULL;
