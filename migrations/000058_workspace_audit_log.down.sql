-- 000058_workspace_audit_log down: drop the workspace audit log table.
-- Idempotent: safe to re-run.
DROP TABLE IF EXISTS workspace_audit_log;
