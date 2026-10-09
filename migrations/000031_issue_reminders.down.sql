-- 000031_issue_reminders down: drop the issue_reminders table and its index.
DROP INDEX IF EXISTS idx_issue_reminders_last_reminded;
DROP TABLE IF EXISTS issue_reminders;
