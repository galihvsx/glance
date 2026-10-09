-- 000022_share_links: public share links for issues and pages (C4T5).
--
-- share_links: one row per share link. The token is a url-safe random
-- 24-byte value (base64url, no padding — 32 chars) generated with
-- crypto/rand by the service; uniqueness is enforced by the UNIQUE
-- constraint (the service retries on the astronomically unlikely
-- collision). scope is 'issue' or 'page'; resource_id is the issue or
-- page id (no FK — the two scopes point at different tables; the
-- service validates liveness at lookup time). workspace_id/project_id
-- are denormalized for the management endpoints' tenancy checks.
-- expires_at NULL = never expires; revoked_at NULL = active.
--
-- The public endpoint looks up by token with a single indexed query
-- and serves a SANITIZED payload (see internal/service/share.go) —
-- the token is the only capability, so the table must never leak
-- through any other endpoint.
--
-- Everything is IF NOT EXISTS so the migration is re-runnable.
CREATE TABLE IF NOT EXISTS share_links (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    token        TEXT NOT NULL UNIQUE,
    scope        TEXT NOT NULL CHECK (scope IN ('issue', 'page')),
    resource_id  UUID NOT NULL,
    workspace_id UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    project_id   UUID REFERENCES projects(id) ON DELETE CASCADE,
    created_by   UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at   TIMESTAMPTZ,
    revoked_at   TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_share_links_resource
    ON share_links(scope, resource_id, created_at DESC);
