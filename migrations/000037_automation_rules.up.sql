-- 000037_automation_rules: per-project workflow automation rules (C11T1).
--
-- A rule fires when an issue's state changes and the transition matches
-- the trigger filter. Actions run in order inside the state-change
-- transaction; an action failure is logged, never blocks the state
-- change. Automation-driven changes never re-trigger automations
-- (depth-1 loop guard, enforced in internal/service/automation.go).
--
-- trigger (JSONB, v1): {"type":"issue.state_changed",
--   "from_states": [<state uuid>, ...] | null,
--   "to_states":   [<state uuid>, ...] | null}
--   null (or empty) means "any".
-- actions (JSONB, ordered array of exactly one of):
--   {"type":"assign","user_id":"<uuid>"}
--   {"type":"add_label","label_id":"<uuid>"}
--   {"type":"add_comment","body":"<text>"}
--
-- Scope guardrail: max 25 rules per project (enforced in service, 409
-- beyond). Rules die with their project and with their author
-- (ON DELETE CASCADE on both — same convention as digest_watermarks).
--
-- IF NOT EXISTS / IF NOT EXISTS index so the migration is re-runnable.
CREATE TABLE IF NOT EXISTS automation_rules (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id UUID NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    trigger    JSONB NOT NULL,
    actions    JSONB NOT NULL,
    enabled    BOOLEAN NOT NULL DEFAULT true,
    created_by UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_automation_rules_project
    ON automation_rules (project_id);
