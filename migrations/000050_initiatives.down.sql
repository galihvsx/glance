-- C27T0 down: drop initiatives (membership junction first).
DROP INDEX IF EXISTS initiative_projects_project_idx;
DROP TABLE IF EXISTS initiative_projects;
DROP INDEX IF EXISTS initiatives_workspace_idx;
DROP TABLE IF EXISTS initiatives;
