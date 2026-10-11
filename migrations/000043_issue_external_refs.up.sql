-- C17T0: two-way GitHub linkage.
--
-- issue_external_refs records PR/commit links on issues. Rows are created
-- manually (POST .../issues/:uuid/refs) or automatically by the GitHub
-- repo webhook (pull_request opened/closed auto-links when the PR title or
-- body mentions the issue's display ID, e.g. GLC-123).
--
-- workspaces.github_webhook_secret holds the repo-level webhook secret
-- used to verify the X-Hub-Signature-256 HMAC on incoming deliveries.
-- Write-only: the API never returns it, only github_webhook_configured.
CREATE TABLE IF NOT EXISTS issue_external_refs (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    issue_id   UUID NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
    provider   TEXT NOT NULL DEFAULT 'github',
    repo       TEXT NOT NULL,            -- owner/name
    ref_type   TEXT NOT NULL,            -- pr | commit
    ref_id     TEXT NOT NULL,            -- PR number / commit SHA
    url        TEXT NOT NULL,
    state      TEXT NOT NULL DEFAULT 'open',  -- open | merged | closed
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT chk_issue_external_refs_type CHECK (ref_type IN ('pr', 'commit')),
    CONSTRAINT chk_issue_external_refs_state CHECK (state IN ('open', 'merged', 'closed'))
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_issue_external_refs_unique
    ON issue_external_refs (issue_id, provider, ref_type, ref_id);
CREATE INDEX IF NOT EXISTS idx_issue_external_refs_issue
    ON issue_external_refs (issue_id);

ALTER TABLE workspaces ADD COLUMN IF NOT EXISTS github_webhook_secret TEXT;
