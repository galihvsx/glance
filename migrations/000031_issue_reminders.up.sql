-- 000031_issue_reminders: due-date reminder state (C8T6).
--
-- issue_reminders: one row per issue the daily reminder job has notified
-- about. issue_id is the PK and cascades on issue delete, so deleting an
-- issue removes its reminder state with it. kind records the last reminder
-- kind sent ('due_soon' | 'overdue'); last_reminded_at is written from the
-- job's explicit clock (not now()) so the 24h overdue throttle is
-- deterministic under a test clock.
-- IF NOT EXISTS so the migration is re-runnable.
CREATE TABLE IF NOT EXISTS issue_reminders (
    issue_id         UUID PRIMARY KEY REFERENCES issues (id) ON DELETE CASCADE,
    last_reminded_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    kind             TEXT NOT NULL DEFAULT 'due_soon'
);

CREATE INDEX IF NOT EXISTS idx_issue_reminders_last_reminded
    ON issue_reminders (last_reminded_at);
