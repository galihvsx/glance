-- 000053_capacity_alerts: capacity-overload alert dedupe state
-- (cycle 29 "Workload & capacity", C29T2).
--
-- capacity_alerts: one row per (workspace, member, calendar day) the
-- daily capacity pass has alerted about. The PK is the dedupe key
-- (member + day); the claim is a conditional INSERT ...
-- ON CONFLICT DO NOTHING RETURNING inside the alert tx, so two
-- concurrent passes converge on one notification per member per day.
-- alert_date is a DATE because the dedupe is calendar-day based (the
-- reminder job's 24h throttle uses TIMESTAMPTZ; here a fresh day is a
-- fresh alert, so a day boundary is the natural key).
--
-- Cascades on workspace/user delete, like digest_watermarks and
-- issue_reminders. Idempotent: safe to re-run.
CREATE TABLE IF NOT EXISTS capacity_alerts (
    workspace_id UUID NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    user_id      UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    alert_date   DATE NOT NULL,
    alerted_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, user_id, alert_date)
);

CREATE INDEX IF NOT EXISTS idx_capacity_alerts_date
    ON capacity_alerts (alert_date);
