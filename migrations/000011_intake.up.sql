-- 000011_intake: intake inbox + triage (spec §4, Task 20).
--
-- Every project owns exactly one default intake (is_default). CreateProject
-- seeds it in the same tx as the states and the issue sequence counter;
-- the INSERT ... SELECT below backfills projects that predate this
-- migration. Issues opt into the inbox with intake:true on create, which
-- writes one intake_issues row in status pending.
--
-- Status encoding (SMALLINT, spec §4 vocabulary):
--   0 pending, 1 rejected, 2 snoozed, 3 accepted, 4 duplicate.
-- Triage terminal states are rejected/accepted/duplicate; pending and
-- snoozed are live. Snoozed rows with snoozed_till <= now() resurface in
-- the inbox at READ time (no background ticker flips rows in v1).
CREATE TABLE IF NOT EXISTS intake (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id   UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name         TEXT NOT NULL DEFAULT 'Intake',
    description  TEXT,
    is_default   BOOL NOT NULL DEFAULT false,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS intake_default_uniq
    ON intake (project_id) WHERE is_default;

CREATE TABLE IF NOT EXISTS intake_issues (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    intake_id       UUID NOT NULL REFERENCES intake(id) ON DELETE CASCADE,
    issue_id        UUID NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
    status          SMALLINT NOT NULL DEFAULT 0 CHECK (status BETWEEN 0 AND 4),
    snoozed_till    TIMESTAMPTZ,
    duplicate_to_id UUID REFERENCES issues(id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (issue_id)
);
CREATE INDEX IF NOT EXISTS intake_issues_intake_idx
    ON intake_issues (intake_id, status, created_at);
CREATE INDEX IF NOT EXISTS intake_issues_issue_idx
    ON intake_issues (issue_id);

-- Backfill: one default intake per pre-existing project. Idempotent
-- (NOT EXISTS guard), so it is safe to re-run; the service test
-- re-executes exactly this statement to prove it.
INSERT INTO intake (project_id, name, is_default)
SELECT p.id, 'Intake', true
FROM projects p
WHERE NOT EXISTS (
    SELECT 1 FROM intake i WHERE i.project_id = p.id AND i.is_default
);
