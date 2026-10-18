-- 000067_workflow_transitions: per-project state-transition allowlist (C68T0).
--
-- Plane paywalls workflow transition controls (Business/Enterprise Grid);
-- glance ships them free. Each row allows one directed state move
-- (from_state_id → to_state_id) inside a project. A project with ZERO
-- rule rows is unrestricted (backwards compatible: existing projects
-- behave exactly as today until an admin configures rules).
--
-- Both endpoints reference states(id) ON DELETE CASCADE: deleting a
-- state drops the rule rows that mention it, so rules can never dangle
-- (a dangling rule would 500 the enforcement check). State renames are
-- unaffected (rules key on state IDs, not names). project_id is
-- denormalized for the per-project lookup; states already carry their
-- own project_id, and SetWorkflow validates every endpoint belongs to
-- the project before insert.
--
-- Idempotent: safe to re-run (CREATE TABLE / UNIQUE / index IF NOT EXISTS).
CREATE TABLE IF NOT EXISTS project_state_transitions (
    from_state_id UUID NOT NULL REFERENCES states(id) ON DELETE CASCADE,
    to_state_id   UUID NOT NULL REFERENCES states(id) ON DELETE CASCADE,
    project_id    UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    UNIQUE (from_state_id, to_state_id)
);
-- Lookup: "the rules of a project, from a given state" (the enforcement
-- check loads the project's whole rule set in one query).
CREATE INDEX IF NOT EXISTS project_state_transitions_project_idx
    ON project_state_transitions (project_id, from_state_id);
