-- 000040_admin_audit_log: append-only instance-admin audit log (C15T1).
--
-- Every admin mutation (user deactivate/reactivate/role change,
-- workspace delete) appends one row, written inside the same
-- transaction as the mutation so the log cannot drift from the
-- underlying change.
--
-- Append-only by convention: the API exposes no update or delete for
-- audit_log, and neither does the service layer. There is no row-level
-- REVOKE because the down migration (re-create after down) would fight
-- it; operator deletes, if ever needed, are a direct SQL affair.
--
-- actor_id is SET NULL on user delete: the log entry survives the
-- actor being removed, and the reader resolves actor_email via a LEFT
-- JOIN. workspace_id is nullable (user-scoped actions have none) and
-- SET NULL on workspace delete.
CREATE TABLE IF NOT EXISTS audit_log (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    actor_id     UUID REFERENCES users(id) ON DELETE SET NULL,
    action       TEXT NOT NULL,
    entity_type  TEXT NOT NULL,
    entity_id    TEXT NOT NULL,
    workspace_id UUID REFERENCES workspaces(id) ON DELETE SET NULL,
    ip           TEXT,
    meta         JSONB NOT NULL DEFAULT '{}'::jsonb
);

-- Newest-first reads are the only access pattern, filtered by
-- action / actor_id / entity_type.
CREATE INDEX IF NOT EXISTS idx_audit_log_at ON audit_log (at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_log_action ON audit_log (action);
CREATE INDEX IF NOT EXISTS idx_audit_log_actor_id ON audit_log (actor_id);
