-- 000034_workspace_slack: Slack incoming-webhook URL per workspace (C9T3).
--
-- Persistence choice: glance has no workspace settings table or KV
-- mechanism (migrations 000001–000033 contain none), so the URL lives
-- as a single TEXT column on workspaces rather than a new settings
-- table — one value, no join, and it inherits the workspace row's
-- tenancy naturally. NULL = not configured (no Slack deliveries).
--
-- The URL is a secret: it is never serialized into API responses (the
-- GET/PATCH responses carry slack_configured, a boolean, instead).
--
-- IF NOT EXISTS keeps the migration idempotent / re-runnable.
ALTER TABLE workspaces
    ADD COLUMN IF NOT EXISTS slack_webhook_url TEXT;
