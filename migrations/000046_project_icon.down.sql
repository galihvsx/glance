-- C18T4 down: drop the project icon column. Idempotent: safe to re-run.
ALTER TABLE projects DROP COLUMN IF EXISTS icon;
