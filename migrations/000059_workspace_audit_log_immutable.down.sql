-- 000059_workspace_audit_log_immutable down: drop the tamper-evidence
-- trigger and function, and clear the table comment (it documents the
-- trigger; without the trigger it would be stale). The table itself
-- and its rows survive — rolling back the guard never deletes history.
-- Idempotent: safe to re-run.
DROP TRIGGER IF EXISTS workspace_audit_log_immutable ON workspace_audit_log;
DROP FUNCTION IF EXISTS workspace_audit_log_block_mutation();
COMMENT ON TABLE workspace_audit_log IS NULL;
