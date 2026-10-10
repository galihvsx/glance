-- 000039_automation_run_retention down: drop the retention window column.
ALTER TABLE projects DROP COLUMN IF EXISTS automation_run_retention_days;
