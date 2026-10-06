-- 000012_cycle: cycles + rollover (spec §4, Task 22).
--
-- Cycles are PROJECT-scoped (project_id FK). status vocabulary:
--   upcoming  — start_date is in the future
--   current   — today is within [start_date, end_date]
--   completed — end_date has passed (set by the in-process ticker,
--               internal/ticker/ticker.go, never by hand)
-- progress_snapshot is a frozen counts-by-state-group JSONB written by the
-- ticker when it completes a cycle; GET .../cycles/{id} computes the live
-- snapshot in one query instead of reading this column.
--
-- Membership follows spec §4 exactly: cycle_issues is the junction table
-- (PK both). An issue may appear in multiple cycles over its lifetime
-- (e.g. rolled over from cycle 1 to cycle 2); the ticker moves
-- cycle_issues rows between cycles, never duplicating an issue inside one
-- cycle.
--
-- close_in_days (projects): number of days after a cycle's end_date before
-- the ticker auto-detaches incomplete issues to the project backlog when
-- there is no next cycle to receive them. NULL = 0 = detach at completion.
-- See internal/ticker/ticker.go for the full rollover rule.
--
-- The modules tables ship in this migration with NO handlers or UI
-- (deferred to post-v1) so later work adds no FK churn.
CREATE TABLE IF NOT EXISTS cycles (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id        UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name              TEXT NOT NULL,
    start_date        DATE NOT NULL,
    end_date          DATE NOT NULL,
    status            TEXT NOT NULL DEFAULT 'upcoming'
                      CHECK (status IN ('upcoming','current','completed')),
    progress_snapshot JSONB,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT cycles_dates CHECK (start_date <= end_date),
    UNIQUE (project_id, name)
);
CREATE INDEX IF NOT EXISTS cycles_project_idx
    ON cycles (project_id, status, end_date);

CREATE TABLE IF NOT EXISTS cycle_issues (
    cycle_id   UUID NOT NULL REFERENCES cycles(id) ON DELETE CASCADE,
    issue_id   UUID NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (cycle_id, issue_id)
);
CREATE INDEX IF NOT EXISTS cycle_issues_issue_idx
    ON cycle_issues (issue_id);

-- Modules (epics): tables only, no API/UI in v1 (spec §4 schema, post-v1 UI).
CREATE TABLE IF NOT EXISTS modules (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id  UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    description TEXT,
    status      TEXT NOT NULL DEFAULT 'active'
                CHECK (status IN ('active','completed','archived')),
    lead_id     UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (project_id, name)
);
CREATE TABLE IF NOT EXISTS module_issues (
    module_id  UUID NOT NULL REFERENCES modules(id) ON DELETE CASCADE,
    issue_id   UUID NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (module_id, issue_id)
);
CREATE INDEX IF NOT EXISTS module_issues_issue_idx
    ON module_issues (issue_id);
CREATE TABLE IF NOT EXISTS module_members (
    module_id  UUID NOT NULL REFERENCES modules(id) ON DELETE CASCADE,
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (module_id, user_id)
);
