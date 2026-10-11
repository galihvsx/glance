-- C17T0 down: drop the external-refs table and the webhook secret column.
DROP TABLE IF EXISTS issue_external_refs;
ALTER TABLE workspaces DROP COLUMN IF EXISTS github_webhook_secret;
