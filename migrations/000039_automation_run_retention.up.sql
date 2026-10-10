-- 000039_automation_run_retention: per-project automation run retention
-- window (C13T0). Every rule firing appends a row to automation_runs
-- forever — C12T1 v1 had no pruning (documented honestly in 000038) —
-- so a workspace with active automations grows unboundedly. The daily
-- retention sweep (service.PruneAutomationRuns, driven by
-- ticker.RetentionTicker) deletes runs older than the window.
--
-- NULL = unset = the 90-day default
-- (service.DefaultAutomationRunRetentionDays); 0 = keep forever;
-- negative is rejected at write by the service (like close_in_days).
-- Stored on the projects row like archive_in_days / close_in_days —
-- no new table, no per-rule configuration in v1 (documented honestly).
ALTER TABLE projects
    ADD COLUMN IF NOT EXISTS automation_run_retention_days INT;
