-- C35T0 up: OKRs — workspace-scoped objectives with measurable key
-- results. Two KR kinds: metric (start → target → current, manual
-- progress) and issue-linked (progress auto-derived from linked issues'
-- done-group completion, same done-group semantics as the initiative
-- rollup). Objective progress is COMPUTED in the service, never
-- stored. Deleting an objective cascades its KRs + links; deleting an
-- issue cascades its links; deleting a user NULLs objective owners.
-- Idempotent: safe to re-run.
CREATE TABLE IF NOT EXISTS objectives (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    title        TEXT NOT NULL,
    description  TEXT NULL,
    owner_id     UUID REFERENCES users(id) ON DELETE SET NULL,
    start_date   DATE NULL,
    target_date  DATE NULL,
    status       TEXT NOT NULL CHECK (status IN ('active', 'completed', 'archived')),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, title)
);
CREATE INDEX IF NOT EXISTS objectives_workspace_idx
    ON objectives (workspace_id);

CREATE TABLE IF NOT EXISTS key_results (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    objective_id  UUID NOT NULL REFERENCES objectives(id) ON DELETE CASCADE,
    title         TEXT NOT NULL,
    kind          TEXT NOT NULL CHECK (kind IN ('metric', 'issue')),
    unit          TEXT NULL,
    start_value   NUMERIC NOT NULL DEFAULT 0,
    target_value  NUMERIC NOT NULL,
    current_value NUMERIC NOT NULL DEFAULT 0,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (target_value <> start_value)
);
CREATE INDEX IF NOT EXISTS key_results_objective_idx
    ON key_results (objective_id);

CREATE TABLE IF NOT EXISTS key_result_issues (
    key_result_id UUID NOT NULL REFERENCES key_results(id) ON DELETE CASCADE,
    issue_id      UUID NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
    PRIMARY KEY (key_result_id, issue_id)
);
CREATE INDEX IF NOT EXISTS key_result_issues_kr_idx
    ON key_result_issues (key_result_id);
