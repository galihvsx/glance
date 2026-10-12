-- C28T0 down: drop issue_schedules (indexes first).
DROP INDEX IF EXISTS issue_schedules_active_next_run_idx;
DROP INDEX IF EXISTS issue_schedules_project_idx;
DROP TABLE IF EXISTS issue_schedules;
