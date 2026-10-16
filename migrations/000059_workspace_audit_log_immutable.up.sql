-- 000059_workspace_audit_log_immutable: DB-level tamper-evidence for the
-- workspace audit log (cycle 37, C37T2). C37T0 made the table
-- append-only by convention (no update/delete path in the API or the
-- service layer); this migration enforces it in the database with a
-- BEFORE UPDATE OR DELETE trigger that raises an exception on any
-- direct mutation of a row.
--
-- The trigger MUST NOT break the table's own FK lifecycle, which is
-- part of the 000058 design and is exercised by DeleteWorkspace:
--   * DELETE FROM workspaces cascades into workspace_audit_log
--     (ON DELETE CASCADE) — deleting a workspace legitimately removes
--     its audit rows with it.
--   * DELETE FROM users SET NULLs actor_id (ON DELETE SET NULL) — the
--     row survives with the actor_email snapshot, by design.
-- A naive unconditional trigger would reject both and break workspace
-- deletion. The discriminator is pg_trigger_depth(), verified
-- empirically 2026-10-16 on this cluster: a direct statement fires
-- the trigger at depth 1, while an FK cascade / SET NULL fires it
-- nested inside the FK's own trigger at depth 2. So:
--   * depth 1 (direct DML from a client or the app) → always rejected.
--   * depth > 1 DELETE → allowed: the only way to reach it is the
--     workspace-cascade, since no other FK points at this table.
--   * depth > 1 UPDATE → allowed ONLY in the exact SET NULL shape
--     (only actor_id changes, to NULL); any other nested UPDATE is
--     still rejected.
--
-- Threat model, stated honestly: this guards against app bugs,
-- accidents, and direct DML through an app-level connection. It does
-- NOT guard against a role with DDL privilege (which can DROP TRIGGER)
-- or TRUNCATE (row-level triggers do not fire for TRUNCATE — and the
-- same privilege that can TRUNCATE can drop the trigger). The trigger
-- is tamper-evidence for the application path, not a substitute for
-- least-privilege database roles.
--
-- Idempotent: safe to re-run.
CREATE OR REPLACE FUNCTION workspace_audit_log_block_mutation()
RETURNS trigger AS $$
BEGIN
    IF pg_trigger_depth() > 1 THEN
        -- Nested inside an FK trigger: the table's own lifecycle.
        IF TG_OP = 'DELETE' THEN
            RETURN OLD;
        END IF;
        -- TG_OP = 'UPDATE': the users ON DELETE SET NULL shape only —
        -- actor_id goes to NULL and every other column is untouched.
        IF NEW.actor_id IS NULL AND OLD.actor_id IS NOT NULL
           AND NEW.id IS NOT DISTINCT FROM OLD.id
           AND NEW.workspace_id IS NOT DISTINCT FROM OLD.workspace_id
           AND NEW.at IS NOT DISTINCT FROM OLD.at
           AND NEW.actor_email IS NOT DISTINCT FROM OLD.actor_email
           AND NEW.action IS NOT DISTINCT FROM OLD.action
           AND NEW.entity_type IS NOT DISTINCT FROM OLD.entity_type
           AND NEW.entity_id IS NOT DISTINCT FROM OLD.entity_id
           AND NEW.ip IS NOT DISTINCT FROM OLD.ip
           AND NEW.meta IS NOT DISTINCT FROM OLD.meta THEN
            RETURN NEW;
        END IF;
    END IF;
    RAISE EXCEPTION 'workspace_audit_log is append-only: % is not allowed (direct UPDATE/DELETE rejected; the only sanctioned removal is a future galih-approved retention decision)', TG_OP;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS workspace_audit_log_immutable ON workspace_audit_log;
CREATE TRIGGER workspace_audit_log_immutable
    BEFORE UPDATE OR DELETE ON workspace_audit_log
    FOR EACH ROW EXECUTE FUNCTION workspace_audit_log_block_mutation();

-- The only sanctioned removal is a future galih-approved retention
-- decision (e.g. a retention purge with its own migration). No
-- application code, admin tool, or ad-hoc SQL may delete or rewrite
-- audit rows before then.
COMMENT ON TABLE workspace_audit_log IS 'Append-only, tamper-evident control-plane history (cycle 37). Rows are never updated or deleted by application code; a BEFORE UPDATE OR DELETE trigger enforces this at the DB level, allowing only the FK lifecycle (workspace DELETE cascades its rows, user DELETE NULLs actor_id). The only sanctioned removal is a future galih-approved retention decision.';
