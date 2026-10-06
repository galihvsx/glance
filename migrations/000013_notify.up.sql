-- 000013_notify: in-app notifications, per-event delivery prefs, webhooks.
--
-- notifications: one row per recipient per event. Written synchronously
-- inside the mutation's tx (notifyTx); the realtime hub is told about them
-- after commit (announceNotifications → user:{id} / notification.created).
-- Email copies go through the shared outbox as "email.notification" rows
-- (claimed by the mail dispatcher, which already handles "email.%").
-- webhooks: workspace-level delivery targets; deliveries go through the
-- shared outbox as "webhook.deliver" rows claimed by the webhook
-- dispatcher (service.WebhookDispatcher, "webhook.%" namespace).
CREATE TABLE IF NOT EXISTS notifications (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    type       TEXT NOT NULL,
    title      TEXT NOT NULL,
    payload    JSONB NOT NULL DEFAULT '{}'::jsonb,
    read_at    TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS notifications_user_idx
    ON notifications (user_id, created_at DESC);

-- notification_prefs: per-user, per-event delivery gates. Absent row =
-- defaults (in_app true, email false) — users opt into email explicitly.
CREATE TABLE IF NOT EXISTS notification_prefs (
    user_id UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    event   TEXT NOT NULL,
    in_app  BOOL NOT NULL DEFAULT TRUE,
    email   BOOL NOT NULL DEFAULT FALSE,
    PRIMARY KEY (user_id, event)
);

CREATE TABLE IF NOT EXISTS webhooks (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    url          TEXT NOT NULL,
    secret       TEXT NOT NULL DEFAULT '',
    events       TEXT[] NOT NULL DEFAULT '{}',
    active       BOOL NOT NULL DEFAULT TRUE,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS webhooks_workspace_idx
    ON webhooks (workspace_id);
