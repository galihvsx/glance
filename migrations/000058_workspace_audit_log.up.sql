-- 000058_workspace_audit_log: append-only workspace-scoped audit log
-- (cycle 37, C37T0). Control-plane events only: membership, role,
-- project lifecycle, workspace settings, integrations. The data-plane
-- activity feed (issue_activities) covers issue CRUD/comments; the
-- instance-admin audit_log covers instance-admin mutations.
--
-- Append-only by convention in v1: the API exposes no update or delete
-- for workspace_audit_log, and neither does the service layer (C37T2
-- adds a DB-level BEFORE UPDATE OR DELETE trigger enforcing it).
--
-- actor_email is a snapshot taken at write time: the row survives the
-- actor's account deletion (actor_id is SET NULL), same rationale as
-- the admin audit_log. ip is inet (NULL when the write path has no
-- request IP — see RecordWorkspaceAuditTx). meta NEVER carries a token
-- value, a webhook secret, a full webhook URL (host only), or an OTP.
-- Idempotent: safe to re-run.
CREATE TABLE IF NOT EXISTS workspace_audit_log (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    actor_id     UUID REFERENCES users(id) ON DELETE SET NULL,
    actor_email  TEXT,
    action       TEXT NOT NULL,
    entity_type  TEXT NOT NULL,
    entity_id    TEXT NOT NULL,
    ip           INET,
    meta         JSONB NOT NULL DEFAULT '{}'::jsonb
);

-- Newest-first list is the only access pattern, filtered by action /
-- actor / entity_type / time range.
CREATE INDEX IF NOT EXISTS idx_workspace_audit_log_list
    ON workspace_audit_log (workspace_id, at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_workspace_audit_log_action
    ON workspace_audit_log (workspace_id, action);
CREATE INDEX IF NOT EXISTS idx_workspace_audit_log_actor
    ON workspace_audit_log (workspace_id, actor_id);
