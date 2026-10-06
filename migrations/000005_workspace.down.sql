-- 000005_workspace down: drop membership before workspaces (FK order).
DROP TABLE IF EXISTS workspace_members;
DROP TABLE IF EXISTS workspaces;
