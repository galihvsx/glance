-- 000034_workspace_slack down: drop the slack_webhook_url column.
ALTER TABLE workspaces
    DROP COLUMN IF EXISTS slack_webhook_url;
