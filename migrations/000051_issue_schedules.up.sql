-- 000051_issue_schedules: recurring issue schedules (cycle 28
-- "Recurring work"). Beyond Plane: Plane has no native scheduled issue
-- creation; glance ships it free (Apache-2.0, no gated features).
--
-- An issue_schedules row is a cron-like recipe scoped to one project:
-- every occurrence the ticker creates one issue from title/description
-- templates rendered with the run time. Schedule times are UTC in v1
-- (documented in the service); per-user timezones are a later cycle.
--
-- days_of_week (0=Sunday..6=Saturday) applies to weekly schedules only;
-- day_of_month (1..31) applies to monthly ones. next_run_at is computed
-- by the service via NextOccurrence(); the ticker advances it after each
-- run (and deactivates the schedule when the occurrences exhaust
-- ends_on).
--
-- state_id NULL means "the project's default state at run time"
-- (the project's backlog state, resolved when the issue is created —
-- NOT frozen at schedule creation). created_by is SET NULL (not
-- CASCADE) so deleting a user never deletes a schedule. Idempotent:
-- safe to re-run.
CREATE TABLE IF NOT EXISTS issue_schedules (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id         UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    project_id           UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name                 TEXT NOT NULL,
    active               BOOLEAN NOT NULL DEFAULT true,
    frequency            TEXT NOT NULL
                         CHECK (frequency IN ('daily','weekly','monthly')),
    interval             INT NOT NULL DEFAULT 1
                         CHECK (interval >= 1),
    days_of_week         INT[] NOT NULL DEFAULT '{}',
    day_of_month         INT NULL
                         CHECK (day_of_month IS NULL OR (day_of_month >= 1 AND day_of_month <= 31)),
    hour                 INT NOT NULL DEFAULT 9
                         CHECK (hour >= 0 AND hour <= 23),
    starts_on            DATE NOT NULL,
    ends_on              DATE NULL
                         CHECK (ends_on IS NULL OR ends_on >= starts_on),
    next_run_at          TIMESTAMPTZ NOT NULL,
    last_run_at          TIMESTAMPTZ NULL,
    run_count            INT NOT NULL DEFAULT 0,
    title_template       TEXT NOT NULL,
    description_template TEXT NULL,
    state_id             UUID NULL REFERENCES states(id) ON DELETE SET NULL,
    priority             INT NOT NULL DEFAULT 0
                         CHECK (priority >= 0 AND priority <= 4),
    label_ids            UUID[] NOT NULL DEFAULT '{}',
    assignee_ids         UUID[] NOT NULL DEFAULT '{}',
    created_by           UUID NULL REFERENCES users(id) ON DELETE SET NULL,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- Lookup: "schedules of a project" (CRUD list path).
CREATE INDEX IF NOT EXISTS issue_schedules_project_idx
    ON issue_schedules (workspace_id, project_id);
-- Lookup: "due schedules" for the ticker (active, ordered by next run).
CREATE INDEX IF NOT EXISTS issue_schedules_active_next_run_idx
    ON issue_schedules (active, next_run_at);
