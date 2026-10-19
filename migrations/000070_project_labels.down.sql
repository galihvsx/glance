-- 000070_project_labels down: drop the project-label tables (manual
-- rollback only; the migrator never applies down files).

DROP TABLE IF EXISTS project_project_labels;
DROP TABLE IF EXISTS project_labels;
